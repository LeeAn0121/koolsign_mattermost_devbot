package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// ── 구조체 ──

type GiteaUser struct {
	Login string `json:"login"`
}

type GiteaRepo struct {
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
}

type GiteaPushEvent struct {
	Ref     string    `json:"ref"`
	Pusher  GiteaUser `json:"pusher"`
	Repo    GiteaRepo `json:"repository"`
	Commits []struct {
		ID      string    `json:"id"`
		Message string    `json:"message"`
		URL     string    `json:"url"`
		Author  GiteaUser `json:"author"`
	} `json:"commits"`
}

type GiteaPREvent struct {
	Action      string `json:"action"`
	PullRequest struct {
		Title   string    `json:"title"`
		HTMLURL string    `json:"html_url"`
		User    GiteaUser `json:"user"`
		Base    struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Merged bool `json:"merged"`
	} `json:"pull_request"`
	Repo GiteaRepo `json:"repository"`
}

type GiteaIssueEvent struct {
	Action string `json:"action"`
	Issue  struct {
		Title   string    `json:"title"`
		HTMLURL string    `json:"html_url"`
		User    GiteaUser `json:"user"`
	} `json:"issue"`
	Repo GiteaRepo `json:"repository"`
}

type GiteaReleaseEvent struct {
	Action  string `json:"action"`
	Release struct {
		TagName string    `json:"tag_name"`
		Name    string    `json:"name"`
		HTMLURL string    `json:"html_url"`
		Author  GiteaUser `json:"author"`
	} `json:"release"`
	Repo GiteaRepo `json:"repository"`
}

// ── HMAC 검증 ──

func hmacSHA256Bytes(secret string, data []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyGiteaSecret(r *http.Request, body []byte, src string) bool {
	secretKey := "GITEA1_SECRET"
	if src == "gitea2" {
		secretKey = "GITEA2_SECRET"
	}
	secret := os.Getenv(secretKey)
	if secret == "" {
		return true
	}
	sig := r.Header.Get("X-Gitea-Signature")
	expected := hmacSHA256Bytes(secret, body)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		log.Printf("[Gitea] 서명 불일치! (Source: %s) 받은서명: %s, 예상서명: %s", src, sig, expected)
		return false
	}
	return true
}

// ── 핸들러 ──

func handleGitea(w http.ResponseWriter, r *http.Request) {
	// 1. 브라우저 접속(GET) 시 연결 확인용 응답 (시크릿 검증 없이 통과)
	if r.Method == http.MethodGet {
		w.WriteHeader(200)
		w.Write([]byte("✅ Gitea Webhook Endpoint is Ready (mm-bot)"))
		return
	}

	// 2. 이후 로직은 웹훅(POST) 요청 처리
	event := strings.ToLower(r.Header.Get("X-Gitea-Event"))
	src := r.URL.Query().Get("src")
	log.Printf("[Gitea] 요청 수신 — Event: %s, Source: %s", event, src)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[Gitea] 바디 읽기 실패: %v", err)
		http.Error(w, "Bad Request", 400)
		return
	}

	if !verifyGiteaSecret(r, body, src) {
		// verifyGiteaSecret 내부에서 이미 상세 로그를 찍음
		http.Error(w, "Unauthorized", 401)
		return
	}

	icon, srcLabel := "🟠", "Gitea-1"
	if src == "gitea2" {
		icon, srcLabel = "🟣", "Gitea-2"
	}

	mmCode := os.Getenv("MM_WEBHOOK_CODE")
	mmDeploy := os.Getenv("MM_WEBHOOK_DEPLOY")

	switch event {
	case "ping":
		log.Printf("[Gitea] Ping 수신 — 연결 성공!")
		w.WriteHeader(200)
		w.Write([]byte("pong"))
		return

	case "push":
		var e GiteaPushEvent
		if err := json.Unmarshal(body, &e); err != nil {
			log.Printf("[Gitea] JSON 파싱 에러 (push): %v | Body: %s", err, string(body))
			break
		}
		if len(e.Commits) == 0 {
			log.Printf("[Gitea] 커밋 0개 (브랜치 생성/삭제/WebUI 특성 등) — Body 확인 필요: %s", string(body))
			break
		}
		log.Printf("[Gitea] Push 알림 전송 시작: %s (%d commits)", e.Repo.Name, len(e.Commits))

		branch := extractBranch(e.Ref)
		branchBadge := fmt.Sprintf("`%s`", branch)
		if branch == "main" || branch == "master" {
			branchBadge = fmt.Sprintf("⚠️ `%s`", branch)
		}

		msg := fmt.Sprintf(
			"### %s %s Push\n**저장소:** %s | **브랜치:** %s | **작성자:** %s\n",
			icon, srcLabel,
			shortRepo(e.Repo.Name, e.Repo.HTMLURL),
			branchBadge, e.Pusher.Login,
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
			msg += fmt.Sprintf("- [`%s`](%s) %s\n", sha, c.URL, firstLine(c.Message))
		}
		if len(e.Commits) > 5 {
			msg += fmt.Sprintf("*...외 %d개 커밋*\n", len(e.Commits)-5)
		}
		sendMM(mmCode, msg)

	case "pull_request":
		var e GiteaPREvent
		if err := json.Unmarshal(body, &e); err != nil {
			break
		}

		labels := map[string]string{
			"opened":   "🔀 PR 오픈",
			"closed":   "🔒 PR 닫힘",
			"reopened": "🔄 PR 재오픈",
		}
		// closed + merged = 병합
		label, ok := labels[e.Action]
		if !ok {
			break
		}
		if e.Action == "closed" && e.PullRequest.Merged {
			label = "✅ PR 병합"
		}

		msg := fmt.Sprintf(
			"### %s %s — %s\n**저장소:** %s\n**제목:** [%s](%s)\n**브랜치:** `%s` → `%s`\n**작성자:** %s",
			icon, label, srcLabel,
			shortRepo(e.Repo.Name, e.Repo.HTMLURL),
			e.PullRequest.Title, e.PullRequest.HTMLURL,
			e.PullRequest.Head.Ref, e.PullRequest.Base.Ref,
			e.PullRequest.User.Login,
		)
		sendMM(mmCode, msg)

	case "issues":
		var e GiteaIssueEvent
		if err := json.Unmarshal(body, &e); err != nil {
			break
		}
		if e.Action != "opened" {
			break
		}

		msg := fmt.Sprintf(
			"### %s 🐛 %s 이슈 오픈\n**저장소:** %s\n**제목:** [%s](%s)\n**작성자:** %s",
			icon, srcLabel,
			shortRepo(e.Repo.Name, e.Repo.HTMLURL),
			e.Issue.Title, e.Issue.HTMLURL,
			e.Issue.User.Login,
		)
		sendMM(mmCode, msg)

	case "release":
		var e GiteaReleaseEvent
		if err := json.Unmarshal(body, &e); err != nil {
			break
		}
		if e.Action != "published" {
			break
		}

		msg := fmt.Sprintf(
			"### %s 🏷 %s 릴리즈\n**저장소:** %s\n**태그:** `%s` — [%s](%s)\n**작성자:** %s",
			icon, srcLabel,
			shortRepo(e.Repo.Name, e.Repo.HTMLURL),
			e.Release.TagName, e.Release.Name, e.Release.HTMLURL,
			e.Release.Author.Login,
		)
		sendMM(mmDeploy, msg)
	}

	// 공통 임포트 사용 선언
	_ = strings.TrimPrefix

	w.WriteHeader(200)
}
