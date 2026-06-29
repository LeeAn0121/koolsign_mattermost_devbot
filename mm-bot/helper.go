package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type MMPayload struct {
	Text      string `json:"text"`
	Username  string `json:"username,omitempty"`
	IconEmoji string `json:"icon_emoji,omitempty"`
}

func sendMM(webhookURL, text string) {
	if webhookURL == "" || webhookURL == "https://m.koolsign.net/hooks/" {
		log.Printf("[sendMM] Webhook URL 미설정 — 메시지 생략: %s", text[:min(len(text), 50)])
		return
	}
	payload := MMPayload{
		Text:      text,
		Username:  "DevBot",
		IconEmoji: ":robot_face:",
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Printf("[sendMM] 마터모스트 전송 실패 (네트워크/URL): %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[sendMM] 마터모스트 응답 에러 (Status: %d): %s", resp.StatusCode, string(respBody))
	} else {
		log.Printf("[sendMM] 마터모스트 전송 완료! (Status: 200)")
	}
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return "...(앞부분 생략)...\n" + string(runes[len(runes)-max:])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func slashResponse(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"response_type": "in_channel",
		"text":          text,
	})
}

func slashEphemeral(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"response_type": "ephemeral",
		"text":          text,
	})
}

func extractBranch(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func firstLine(s string) string {
	lines := strings.SplitN(s, "\n", 2)
	return strings.TrimSpace(lines[0])
}

func shortRepo(name, url string) string {
	return fmt.Sprintf("[%s](%s)", name, url)
}

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func runDockerLog(container, lines string) (string, error) {
	out, err := exec.Command("docker", "logs", "--tail", lines, container).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("컨테이너 없음: %s", container)
	}
	return truncate(string(out), 3000), nil
}

func runScript(scriptPath string) ([]byte, error) {
	return exec.Command("/bin/sh", scriptPath).CombinedOutput()
}

// parseAnnounceArgs 는 공지 명령어 인자를 파싱합니다.
// 입력 형식: [~채널명] [HH:MM] 메시지내용
// 반환: channelName(없으면 ""), scheduleTime(없으면 nil), message, error
func parseAnnounceArgs(text string) (channelName string, schedAt *time.Time, message string, err error) {
	parts := strings.Fields(text)
	idx := 0

	// 1. ~채널명
	if idx < len(parts) && strings.HasPrefix(parts[idx], "~") {
		channelName = strings.TrimPrefix(parts[idx], "~")
		idx++
	}

	// 2. HH:MM 시간
	if idx < len(parts) {
		at, e := parseScheduleTime(parts[idx])
		if e == nil {
			schedAt = &at
			idx++
		}
	}

	// 3. 메시지
	message = strings.Join(parts[idx:], " ")
	if message == "" {
		err = fmt.Errorf("공지 내용이 비어 있습니다")
	}
	return
}

// resolveChannelID 는 팀 ID와 채널명으로 채널 ID를 반환합니다.
// channelName 앞의 ~ 또는 # 은 자동으로 제거됩니다.
func resolveChannelID(teamID, channelName string) (string, error) {
	channelName = strings.TrimPrefix(channelName, "~")
	channelName = strings.TrimPrefix(channelName, "#")
	channelName = strings.ToLower(strings.TrimSpace(channelName))

	if mmClient == nil {
		return "", fmt.Errorf("Bot이 초기화되지 않았습니다")
	}

	// teamID가 있으면 바로 조회
	if teamID != "" {
		ch, _, err := mmClient.GetChannelByName(channelName, teamID, "")
		if err != nil || ch == nil {
			return "", fmt.Errorf("채널을 찾을 수 없습니다: `~%s`\n채널명은 영문 소문자로 입력하세요.", channelName)
		}
		return ch.Id, nil
	}

	// teamID 없으면 Bot 자신의 팀 전체 순회
	if botUser == nil {
		return "", fmt.Errorf("Bot이 아직 초기화되지 않았습니다. 잠시 후 다시 시도하세요.")
	}
	teams, _, err := mmClient.GetTeamsForUser(botUser.Id, "")
	if err != nil || len(teams) == 0 {
		return "", fmt.Errorf("Bot이 속한 팀을 찾을 수 없습니다")
	}
	for _, team := range teams {
		ch, _, err := mmClient.GetChannelByName(channelName, team.Id, "")
		if err == nil && ch != nil {
			return ch.Id, nil
		}
	}
	return "", fmt.Errorf("채널을 찾을 수 없습니다: `~%s`\n채널명은 영문 소문자로 입력하세요.", channelName)
}
