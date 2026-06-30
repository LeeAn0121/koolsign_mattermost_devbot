package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// ── 구조체 ──

type GitlabPushEvent struct {
	Ref               string `json:"ref"`
	UserName          string `json:"user_name"`
	TotalCommitsCount int    `json:"total_commits_count"`
	Project           struct {
		Name   string `json:"name"`
		WebURL string `json:"web_url"`
	} `json:"project"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		URL     string `json:"url"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commits"`
}

type GitlabMREvent struct {
	User struct {
		Name string `json:"name"`
	} `json:"user"`
	Project struct {
		Name   string `json:"name"`
		WebURL string `json:"web_url"`
	} `json:"project"`
	ObjectAttributes struct {
		Title        string `json:"title"`
		URL          string `json:"url"`
		Action       string `json:"action"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
	} `json:"object_attributes"`
	Assignees []struct {
		Name string `json:"name"`
	} `json:"assignees"`
	Reviewers []struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"reviewers"`
}

type GitlabIssueEvent struct {
	User struct {
		Name string `json:"name"`
	} `json:"user"`
	Project struct {
		Name   string `json:"name"`
		WebURL string `json:"web_url"`
	} `json:"project"`
	ObjectAttributes struct {
		Title  string `json:"title"`
		URL    string `json:"url"`
		Action string `json:"action"`
	} `json:"object_attributes"`
}

type GitlabPipelineEvent struct {
	ObjectAttributes struct {
		Status string `json:"status"`
		Ref    string `json:"ref"`
		SHA    string `json:"sha"`
	} `json:"object_attributes"`
	Project struct {
		Name   string `json:"name"`
		WebURL string `json:"web_url"`
	} `json:"project"`
	User struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"user"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

type GitlabTagEvent struct {
	Ref      string `json:"ref"`
	UserName string `json:"user_name"`
	Project  struct {
		Name   string `json:"name"`
		WebURL string `json:"web_url"`
	} `json:"project"`
}

// ── 핸들러 ──

func handleGitlab(w http.ResponseWriter, r *http.Request) {
	// 시크릿 검증
	secret := os.Getenv("GITLAB_SECRET")
	receivedToken := r.Header.Get("X-Gitlab-Token")
	if secret != "" && receivedToken != secret {
		log.Printf("[Gitlab] Unauthorized: Token mismatch. Expected: %s, Received: %s", secret, receivedToken)
		http.Error(w, "Unauthorized", 401)
		return
	}

	event := strings.TrimSpace(r.Header.Get("X-Gitlab-Event"))
	log.Printf("[Gitlab] 요청 수신 — Event: [%s]", event)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[Gitlab] 바디 읽기 실패: %v", err)
		http.Error(w, "Bad Request", 400)
		return
	}

	// System Hook 처리: 본문에서 실제 이벤트를 찾아 event 변수 갱신
	if strings.EqualFold(event, "System Hook") {
		var sys struct {
			ObjectKind string `json:"object_kind"`
			EventName  string `json:"event_name"`
		}
		if err := json.Unmarshal(body, &sys); err != nil {
			log.Printf("[Gitlab] System Hook JSON 파싱 실패: %v | Body: %s", err, string(body))
		} else {
			kind := sys.ObjectKind
			if kind == "" {
				kind = sys.EventName
			} // object_kind가 없으면 event_name 사용

			log.Printf("[Gitlab] System Hook 분석: kind=%s, event_name=%s", kind, sys.EventName)

			switch kind {
			case "push":
				event = "Push Hook"
			case "merge_request":
				event = "Merge Request Hook"
			case "issue":
				event = "Issue Hook"
			case "tag_push":
				event = "Tag Push Hook"
			case "pipeline":
				event = "Pipeline Hook"
			}
		}
	}

	mmCode := os.Getenv("MM_WEBHOOK_CODE")
	mmDeploy := os.Getenv("MM_WEBHOOK_DEPLOY")

	switch event {
	case "Push Hook":
		var e GitlabPushEvent
		if err := json.Unmarshal(body, &e); err != nil {
			log.Printf("[Gitlab] JSON 파싱 에러 (Push Hook): %v", err)
			break
		}
		if e.TotalCommitsCount == 0 {
			log.Printf("[Gitlab] TotalCommitsCount 가 0임 (브랜치 삭제 등 무시)")
			break
		}

		branch := extractBranch(e.Ref)
		log.Printf("[Gitlab] Push Hook 처리 중: %s (%d commits)", e.Project.Name, e.TotalCommitsCount)
		branchBadge := fmt.Sprintf("`%s`", branch)
		if branch == "main" || branch == "master" {
			branchBadge = fmt.Sprintf("`%s` [main/master]", branch)
		}

		msg := fmt.Sprintf(
			"### GitLab Push\n**프로젝트:** %s | **브랜치:** %s | **작성자:** %s | **커밋:** %d개\n",
			shortRepo(e.Project.Name, e.Project.WebURL),
			branchBadge, e.UserName, e.TotalCommitsCount,
		)
		limit := 5
		if len(e.Commits) < limit {
			limit = len(e.Commits)
		}
		for _, c := range e.Commits[:limit] {
			sha := c.ID
			if len(sha) > 7 {
				sha = sha[:7]
			}
			msg += fmt.Sprintf("- [`%s`](%s) %s — *%s*\n", sha, c.URL, firstLine(c.Message), c.Author.Name)
		}
		if len(e.Commits) > 5 {
			msg += fmt.Sprintf("*...외 %d개 커밋*\n", len(e.Commits)-5)
		}
		sendMM(mmCode, msg)

	case "Merge Request Hook":
		var e GitlabMREvent
		if err := json.Unmarshal(body, &e); err != nil {
			log.Printf("[Gitlab] JSON 파싱 에러 (MR Hook): %v", err)
			break
		}

		labels := map[string]string{
			"open":   "MR 오픈",
			"close":  "MR 닫힘",
			"merge":  "MR 병합",
			"reopen": "MR 재오픈",
		}
		label, ok := labels[e.ObjectAttributes.Action]
		if !ok {
			log.Printf("[Gitlab] MR Hook 무시: 알 수 없는 action: %s", e.ObjectAttributes.Action)
			break
		}

		log.Printf("[Gitlab] MR Hook 처리 중: %s (%s)", e.Project.Name, e.ObjectAttributes.Action)

		assignee := "없음"
		if len(e.Assignees) > 0 {
			var names []string
			for _, a := range e.Assignees {
				names = append(names, a.Name)
			}
			assignee = strings.Join(names, ", ")
		}

		reviewer := "없음"
		if len(e.Reviewers) > 0 {
			var mentions []string
			for _, r := range e.Reviewers {
				if r.Username != "" {
					mentions = append(mentions, "@"+r.Username)
				} else {
					mentions = append(mentions, r.Name)
				}
			}
			reviewer = strings.Join(mentions, ", ")
		}

		msg := fmt.Sprintf(
			"### %s — GitLab\n**프로젝트:** %s\n**제목:** [%s](%s)\n**브랜치:** `%s` → `%s`\n**작성자:** %s | **담당자:** %s | **리뷰어:** %s",
			label,
			shortRepo(e.Project.Name, e.Project.WebURL),
			e.ObjectAttributes.Title, e.ObjectAttributes.URL,
			e.ObjectAttributes.SourceBranch, e.ObjectAttributes.TargetBranch,
			e.User.Name, assignee, reviewer,
		)
		sendMM(mmCode, msg)

		// MR 병합 시 자동 배포
		if e.ObjectAttributes.Action == "merge" {
			autoTarget := getEnv("AUTO_DEPLOY_MERGE_TO", "")
			autoScript := getEnv("AUTO_DEPLOY_SCRIPT", "")
			if autoTarget != "" && autoScript != "" &&
				strings.EqualFold(e.ObjectAttributes.TargetBranch, autoTarget) {
				go func() {
					mmDeploy := os.Getenv("MM_WEBHOOK_DEPLOY")
					out, err := runScript(autoScript)
					if err != nil {
						sendMM(mmDeploy, fmt.Sprintf(
							"### 자동 배포 실패\n**MR:** [%s](%s)\n**작성자:** %s\n```\n%s\n```",
							e.ObjectAttributes.Title, e.ObjectAttributes.URL, e.User.Name, truncate(string(out), 2000),
						))
					} else {
						sendMM(mmDeploy, fmt.Sprintf(
							"### 자동 배포 완료\n**MR 병합:** [%s](%s)\n**작성자:** %s",
							e.ObjectAttributes.Title, e.ObjectAttributes.URL, e.User.Name,
						))
					}
				}()
			}
		}

	case "Issue Hook":
		var e GitlabIssueEvent
		if err := json.Unmarshal(body, &e); err != nil {
			break
		}
		if e.ObjectAttributes.Action != "open" {
			break
		}

		msg := fmt.Sprintf(
			"### GitLab 이슈 오픈\n**프로젝트:** %s\n**제목:** [%s](%s)\n**작성자:** %s",
			shortRepo(e.Project.Name, e.Project.WebURL),
			e.ObjectAttributes.Title, e.ObjectAttributes.URL,
			e.User.Name,
		)
		sendMM(mmCode, msg)

	case "Pipeline Hook":
		var e GitlabPipelineEvent
		if err := json.Unmarshal(body, &e); err != nil {
			log.Printf("[Gitlab] JSON 파싱 에러 (Pipeline Hook): %v", err)
			break
		}

		status := e.ObjectAttributes.Status
		if status == "running" || status == "pending" {
			log.Printf("[Gitlab] Pipeline Hook 무시 (status: %s)", status)
			break
		}

		log.Printf("[Gitlab] Pipeline Hook 처리 중: %s (%s)", e.Project.Name, status)

		labels := map[string]string{
			"success":  "성공",
			"failed":   "실패",
			"canceled": "취소",
		}
		label := labels[status]
		if label == "" {
			label = status
		}

		msg := fmt.Sprintf(
			"### GitLab Pipeline %s\n**프로젝트:** %s | **브랜치:** `%s`\n**커밋:** %s\n**작성자:** %s",
			label,
			shortRepo(e.Project.Name, e.Project.WebURL),
			e.ObjectAttributes.Ref,
			firstLine(e.Commit.Message),
			e.User.Name,
		)
		sendMM(mmDeploy, msg)
		if status == "failed" {
			go sendDMToUser(e.User.Username, fmt.Sprintf(
				"파이프라인 실패 알림\n**프로젝트:** %s\n**브랜치:** `%s`\n**커밋:** %s",
				e.Project.Name, e.ObjectAttributes.Ref, firstLine(e.Commit.Message),
			))
		}

	case "Tag Push Hook":
		var e GitlabTagEvent
		if err := json.Unmarshal(body, &e); err != nil {
			log.Printf("[Gitlab] JSON 파싱 에러 (Tag Hook): %v", err)
			break
		}
		tag := strings.TrimPrefix(e.Ref, "refs/tags/")
		log.Printf("[Gitlab] Tag Hook 처리 중: %s (%s)", e.Project.Name, tag)
		msg := fmt.Sprintf(
			"### GitLab 태그 생성\n**프로젝트:** %s | **태그:** `%s` | **작성자:** %s",
			shortRepo(e.Project.Name, e.Project.WebURL),
			tag, e.UserName,
		)
		sendMM(mmDeploy, msg)

	default:
		log.Printf("[Gitlab] 알 수 없는 이벤트: %s", event)
	}

	w.WriteHeader(200)
}
