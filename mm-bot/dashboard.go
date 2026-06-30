package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type glMR struct {
	Title  string `json:"title"`
	WebURL string `json:"web_url"`
	Author struct {
		Name string `json:"name"`
	} `json:"author"`
	CreatedAt string `json:"created_at"`
}

type giteaPR struct {
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

func fetchGitLabOpenMRs() ([]glMR, error) {
	token := getEnv("GITLAB_API_TOKEN", "")
	baseURL := getEnv("GITLAB_BASE_URL", "")
	if token == "" || baseURL == "" {
		return nil, fmt.Errorf("GITLAB_API_TOKEN 또는 GITLAB_BASE_URL 미설정")
	}

	apiURL := strings.TrimSuffix(baseURL, "/") + "/api/v4/merge_requests?state=opened&scope=all&per_page=50"
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", token)

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var mrs []glMR
	if err := json.NewDecoder(resp.Body).Decode(&mrs); err != nil {
		return nil, err
	}
	return mrs, nil
}

func fetchGiteaOpenPRs(baseURL, token, ownerRepo string) ([]giteaPR, error) {
	parts := strings.SplitN(ownerRepo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("잘못된 형식: %s (owner/repo 필요)", ownerRepo)
	}

	apiURL := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls?state=open&limit=50",
		strings.TrimSuffix(baseURL, "/"), parts[0], parts[1])
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var prs []giteaPR
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return nil, err
	}
	return prs, nil
}

// getDashboard 는 GitLab + Gitea 열린 MR/PR 현황을 조합해 반환합니다.
// 환경변수:
//
//	GITLAB_API_TOKEN — GitLab Personal Access Token
//	GITLAB_BASE_URL  — GitLab 서버 URL
//	GITEA1_API_TOKEN — Gitea API 토큰
//	GITEA1_BASE_URL  — Gitea 서버 URL
//	GITEA1_REPOS     — 쉼표 구분 저장소 목록 (owner/repo 형식)
func getDashboard() string {
	var sb strings.Builder
	sb.WriteString("### 통합 대시보드\n\n")

	mrs, err := fetchGitLabOpenMRs()
	if err != nil {
		sb.WriteString(fmt.Sprintf("**GitLab:** 조회 실패 — %v\n\n", err))
	} else {
		fmt.Fprintf(&sb, "**GitLab 열린 MR: %d개**\n", len(mrs))
		if len(mrs) > 0 {
			sb.WriteString("| 제목 | 작성자 | 생성일 |\n|---|---|---|\n")
			for _, mr := range mrs {
				created := "-"
				if len(mr.CreatedAt) >= 10 {
					created = mr.CreatedAt[:10]
				}
				fmt.Fprintf(&sb, "| [%s](%s) | %s | %s |\n",
					truncate(mr.Title, 40), mr.WebURL, mr.Author.Name, created)
			}
		}
		sb.WriteString("\n")
	}

	giteaBase := getEnv("GITEA1_BASE_URL", "")
	giteaToken := getEnv("GITEA1_API_TOKEN", "")
	giteaRepos := getEnv("GITEA1_REPOS", "")

	if giteaBase != "" && giteaRepos != "" {
		repoList := strings.Split(giteaRepos, ",")
		var prLines []string
		totalPRs := 0
		for _, repo := range repoList {
			repo = strings.TrimSpace(repo)
			if repo == "" {
				continue
			}
			prs, err := fetchGiteaOpenPRs(giteaBase, giteaToken, repo)
			if err != nil {
				log.Printf("[Dashboard] Gitea %s 조회 실패: %v", repo, err)
				continue
			}
			for _, pr := range prs {
				prLines = append(prLines, fmt.Sprintf("| [%s](%s) | `%s` | %s | %s |\n",
					truncate(pr.Title, 40), pr.HTMLURL, repo, pr.User.Login, pr.CreatedAt.Format("01/02")))
				totalPRs++
			}
		}
		fmt.Fprintf(&sb, "**Gitea 열린 PR: %d개**\n", totalPRs)
		if totalPRs > 0 {
			sb.WriteString("| 제목 | 저장소 | 작성자 | 생성일 |\n|---|---|---|---|\n")
			for _, line := range prLines {
				sb.WriteString(line)
			}
		}
	}

	return sb.String()
}

// startMRReminderScheduler 는 매일 MR_REMINDER_TIME 에 48시간 이상 열린 MR 목록을 전송합니다.
// 환경변수:
//
//	MR_REMINDER_TIME    — HH:MM 형식 (미설정 시 비활성화)
//	MR_REMINDER_WEBHOOK — 알림 Webhook URL (미설정 시 MM_WEBHOOK_CODE 사용)
//	MR_STALE_HOURS      — 스테일 기준 시간 (기본: 48)
func startMRReminderScheduler() {
	reminderTime := getEnv("MR_REMINDER_TIME", "")
	if reminderTime == "" {
		return
	}
	webhookURL := getEnv("MR_REMINDER_WEBHOOK", getEnv("MM_WEBHOOK_CODE", ""))
	staleHours := 48
	if v := getEnv("MR_STALE_HOURS", ""); v != "" {
		var h int
		if _, err := fmt.Sscanf(v, "%d", &h); err == nil && h > 0 {
			staleHours = h
		}
	}

	log.Printf("[MRReminder] 스테일 MR 리마인더 시작 — 매일 %s (%d시간 기준)", reminderTime, staleHours)

	for {
		next, err := parseScheduleTime(reminderTime)
		if err != nil {
			log.Printf("[MRReminder] MR_REMINDER_TIME 형식 오류: %v", err)
			return
		}
		time.Sleep(time.Until(next))

		mrs, err := fetchGitLabOpenMRs()
		if err != nil {
			log.Printf("[MRReminder] GitLab MR 조회 실패: %v", err)
			continue
		}

		cutoff := time.Now().Add(-time.Duration(staleHours) * time.Hour)
		var stale []glMR
		for _, mr := range mrs {
			if len(mr.CreatedAt) < 10 {
				continue
			}
			created, err := time.Parse("2006-01-02T15:04:05Z07:00", mr.CreatedAt)
			if err != nil {
				created, err = time.Parse("2006-01-02", mr.CreatedAt[:10])
				if err != nil {
					continue
				}
			}
			if created.Before(cutoff) {
				stale = append(stale, mr)
			}
		}

		if len(stale) == 0 {
			log.Printf("[MRReminder] 스테일 MR 없음")
			continue
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "### 리뷰 미완료 MR 리마인더 (%d개, %d시간 이상 열림)\n", len(stale), staleHours)
		sb.WriteString("| 제목 | 작성자 | 생성일 |\n|---|---|---|\n")
		for _, mr := range stale {
			created := "-"
			if len(mr.CreatedAt) >= 10 {
				created = mr.CreatedAt[:10]
			}
			fmt.Fprintf(&sb, "| [%s](%s) | %s | %s |\n",
				truncate(mr.Title, 50), mr.WebURL, mr.Author.Name, created)
		}
		sendMM(webhookURL, sb.String())
	}
}
