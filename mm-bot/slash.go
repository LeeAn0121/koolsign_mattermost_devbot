package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

func verifySlashToken(command, token string) bool {
	envKey := map[string]string{
		"/deploy":       "SLASH_TOKEN_DEPLOY",
		"/log":          "SLASH_TOKEN_LOG",
		"/serverstatus": "SLASH_TOKEN_STATUS",
		"/dbquery":      "SLASH_TOKEN_DBQUERY",
		"/schedule":     "SLASH_TOKEN_SCHEDULE",
		"/weather":      "SLASH_TOKEN_WEATHER",
		"/announce":     "SLASH_TOKEN_ANNOUNCE",
		"/commands":     "SLASH_TOKEN_HELP",
	}
	key, ok := envKey[command]
	if !ok {
		return false
	}
	expected := os.Getenv(key)
	if expected == "" {
		return true
	}
	return expected == token
}

func handleSlash(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	command := r.FormValue("command")
	text := r.FormValue("text")
	token := r.FormValue("token")
	userName := r.FormValue("user_name")
	channelID := r.FormValue("channel_id")
	teamID := r.FormValue("team_id")

	if !verifySlashToken(command, token) {
		slashEphemeral(w, "❌ 인증 실패")
		return
	}

	switch command {
	case "/commands":
		handleHelp(w)
	case "/deploy":
		handleDeploy(w, text, userName)
	case "/log":
		handleLog(w, text)
	case "/serverstatus":
		handleStatus(w)
	case "/dbquery":
		handleDBQuery(w, text, userName)
	case "/schedule":
		handleScheduleSlash(w, text, userName, channelID)
	case "/weather":
		handleWeatherSlash(w, text)
	case "/announce":
		handleAnnounceSlash(w, text, userName, channelID, teamID)
	default:
		slashEphemeral(w, "❓ 알 수 없는 명령어")
	}
}

func handleDeploy(w http.ResponseWriter, args, userName string) {
	target := strings.TrimSpace(args)
	scripts := map[string]string{
		"staging": "./scripts/deploy-staging.sh",
		"prod":    "./scripts/deploy-prod.sh",
	}
	scriptPath, ok := scripts[target]
	if !ok {
		slashEphemeral(w, fmt.Sprintf("❌ 알 수 없는 대상: `%s`\n가능: staging, prod", target))
		return
	}

	slashResponse(w, fmt.Sprintf("🚀 **%s** 배포 시작 — @%s\n결과는 `#deploy-log` 채널로 전송됩니다.", target, userName))

	go func() {
		mmDeploy := os.Getenv("MM_WEBHOOK_DEPLOY")
		mmAlert := os.Getenv("MM_WEBHOOK_ALERT")

		out, err := runScript(scriptPath)
		if err != nil {
			errMsg := fmt.Sprintf("### ❌ 배포 실패 — `%s` (by @%s)\n```\n%s\n```",
				target, userName, truncate(string(out), 2000))
			sendMM(mmDeploy, errMsg)
			if target == "prod" {
				sendMM(mmAlert, "## 🚨 [CRITICAL] 프로덕션 배포 실패!\n즉시 확인이 필요합니다.\n"+errMsg)
			}
			return
		}
		sendMM(mmDeploy, fmt.Sprintf("### ✅ 배포 완료 — `%s` (by @%s)\n```\n%s\n```",
			target, userName, truncate(string(out), 2000)))
	}()
}

func handleLog(w http.ResponseWriter, args string) {
	parts := strings.Fields(args)
	if len(parts) < 1 {
		slashEphemeral(w, "사용법: `/log [컨테이너명] [줄수(기본100)]`")
		return
	}
	container := parts[0]
	lines := "100"
	if len(parts) >= 2 {
		lines = parts[1]
	}

	out, err := exec.Command("docker", "logs", "--tail", lines, container).CombinedOutput()
	if err != nil {
		listOut, _ := exec.Command("docker", "ps", "--format", "{{.Names}}").CombinedOutput()
		slashEphemeral(w, fmt.Sprintf("❌ 컨테이너 없음: `%s`\n\n**실행 중:**\n```\n%s\n```",
			container, string(listOut)))
		return
	}
	slashResponse(w, fmt.Sprintf("### 📋 `%s` 최근 %s줄\n```\n%s\n```",
		container, lines, truncate(string(out), 3000)))
}

func handleStatus(w http.ResponseWriter) {
	ps, _ := runCmd("docker", "ps", "--format", "| {{.Names}} | {{.Status}} | {{.Ports}} |")
	df, _ := runCmd("df", "-h", "/")
	mem, _ := runCmd("free", "-h")

	msg := fmt.Sprintf(
		"### 🐳 서버 상태\n\n**컨테이너**\n| 이름 | 상태 | 포트 |\n|---|---|---|\n%s\n\n**디스크**\n```\n%s\n```\n**메모리**\n```\n%s\n```",
		ps, truncate(df, 400), truncate(mem, 400),
	)
	slashResponse(w, msg)
}

func handleDBQuery(w http.ResponseWriter, query, userName string) {
	query = strings.TrimSpace(query)
	if query == "" {
		slashEphemeral(w, "사용법: `/dbquery SELECT * FROM table LIMIT 10`")
		return
	}

	upper := strings.ToUpper(query)
	for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "DROP", "TRUNCATE", "ALTER", "CREATE"} {
		if strings.Contains(upper, kw) {
			slashEphemeral(w, fmt.Sprintf("❌ `%s` 는 허용되지 않습니다. SELECT만 사용 가능합니다.", kw))
			return
		}
	}

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		slashEphemeral(w, "❌ DB_DSN 미설정")
		return
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		slashEphemeral(w, fmt.Sprintf("❌ DB 연결 실패: %v", err))
		return
	}
	defer db.Close()

	rows, err := db.Query(query)
	if err != nil {
		slashEphemeral(w, fmt.Sprintf("❌ 쿼리 오류: %v", err))
		return
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	result := "| " + strings.Join(cols, " | ") + " |\n"
	result += "|" + strings.Repeat("---|", len(cols)) + "\n"

	rowCount := 0
	for rows.Next() {
		if rowCount >= 20 {
			result += "*...최대 20행 표시*\n"
			break
		}
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rows.Scan(ptrs...)

		row := "| "
		for _, v := range vals {
			if v == nil {
				row += "NULL | "
			} else {
				row += fmt.Sprintf("%v | ", v)
			}
		}
		result += row + "\n"
		rowCount++
	}

	slashResponse(w, fmt.Sprintf("### 🗃 DB 조회 (by @%s)\n```sql\n%s\n```\n%s", userName, query, result))
}

// ── 예약 메시지 슬래시 커맨드 ──────────────────────────────────────────
// 사용법: /schedule HH:MM 메시지내용
//
//	/schedule list
//	/schedule cancel [ID]
func handleScheduleSlash(w http.ResponseWriter, args, userName, channelID string) {
	args = strings.TrimSpace(args)
	if args == "" {
		slashEphemeral(w, "사용법:\n"+
			"- `/schedule HH:MM 메시지내용` — 예약 등록\n"+
			"- `/schedule list` — 예약 목록\n"+
			"- `/schedule cancel [ID]` — 예약 취소")
		return
	}

	parts := strings.Fields(args)

	// /schedule list
	if parts[0] == "list" {
		list := listSchedules()
		if len(list) == 0 {
			slashEphemeral(w, "📭 예약된 메시지가 없습니다.")
			return
		}
		msg := "### 📅 예약된 메시지 목록\n| ID | 유형 | 전송시간 | 메시지 |\n|---|---|---|---|\n"
		for _, s := range list {
			kind := "일반"
			if s.IsAnnounce {
				kind = "📢 공지"
			}
			msg += fmt.Sprintf("| %d | %s | %s | %s |\n", s.ID, kind, s.At.Format("01/02 15:04"), truncate(s.Message, 50))
		}
		slashEphemeral(w, msg)
		return
	}

	// /schedule cancel [ID]
	if parts[0] == "cancel" {
		if len(parts) < 2 {
			slashEphemeral(w, "사용법: `/schedule cancel [ID]`")
			return
		}
		var id int
		if _, err := fmt.Sscanf(parts[1], "%d", &id); err != nil {
			slashEphemeral(w, "❌ ID는 숫자여야 합니다.")
			return
		}
		if cancelSchedule(id) {
			slashEphemeral(w, fmt.Sprintf("✅ 예약 메시지 ID **%d** 취소 완료", id))
		} else {
			slashEphemeral(w, fmt.Sprintf("❌ ID **%d** 예약을 찾을 수 없습니다.", id))
		}
		return
	}

	// /schedule HH:MM 메시지내용
	if len(parts) < 2 {
		slashEphemeral(w, "사용법: `/schedule HH:MM 메시지내용`\n예: `/schedule 14:30 서버 점검 시작됩니다`")
		return
	}
	timeStr := parts[0]
	message := strings.Join(parts[1:], " ")

	at, err := parseScheduleTime(timeStr)
	if err != nil {
		slashEphemeral(w, fmt.Sprintf("❌ 시간 형식 오류: `%s` → %v\n(HH:MM 형식 사용, 예: `09:00`)", timeStr, err))
		return
	}
	id := addSchedule(channelID, userName, message, at, false)
	slashEphemeral(w, fmt.Sprintf("✅ 예약 완료! (ID: **%d**)\n- 전송 시간: **%s**\n- 메시지: %s", id, at.Format("01/02 15:04"), message))
}

// ── 날씨 확인 슬래시 커맨드 ──────────────────────────────────────────
// 사용법: /weather [도시명]
func handleWeatherSlash(w http.ResponseWriter, city string) {
	city = strings.TrimSpace(city)
	msg, err := getWeather(city)
	if err != nil {
		slashEphemeral(w, fmt.Sprintf("❌ %v", err))
		return
	}
	slashResponse(w, msg)
}

// ── 공지 메시지 슬래시 커맨드 ──────────────────────────────────────────
// 사용법: /announce 메시지내용
//
//	/announce ~채널명 메시지내용
//	/announce HH:MM 메시지내용
//	/announce ~채널명 HH:MM 메시지내용
func handleAnnounceSlash(w http.ResponseWriter, text, userName, currentChannelID, teamID string) {
	text = strings.TrimSpace(text)
	if text == "" {
		slashEphemeral(w, "사용법:\n"+
			"- `/announce 메시지` — 즉시, 현재 채널\n"+
			"- `/announce ~채널명 메시지` — 즉시, 특정 채널\n"+
			"- `/announce HH:MM 메시지` — 예약, 현재 채널\n"+
			"- `/announce ~채널명 HH:MM 메시지` — 예약, 특정 채널")
		return
	}

	channelName, schedAt, content, err := parseAnnounceArgs(text)
	if err != nil {
		slashEphemeral(w, fmt.Sprintf("❌ %v", err))
		return
	}

	// 채널 결정
	targetChannelID := currentChannelID
	if channelName != "" {
		chID, err := resolveChannelID(teamID, channelName)
		if err != nil {
			slashEphemeral(w, fmt.Sprintf("❌ %v", err))
			return
		}
		targetChannelID = chID
	}

	// 예약 공지
	if schedAt != nil {
		id := addSchedule(targetChannelID, userName, content, *schedAt, true)
		chDesc := "현재 채널"
		if channelName != "" {
			chDesc = fmt.Sprintf("~%s", channelName)
		}
		slashEphemeral(w, fmt.Sprintf("✅ 공지 예약 완료! (ID: **%d**)\n- 채널: %s\n- 전송 시간: **%s**\n- 내용: %s",
			id, chDesc, schedAt.Format("01/02 15:04"), content))
		return
	}

	// 즉시 공지
	announceMsg := buildAnnounceMsg(content, userName)
	if mmClient != nil {
		sendBotMessage(targetChannelID, announceMsg)
		chDesc := "현재 채널"
		if channelName != "" {
			chDesc = fmt.Sprintf("`~%s`", channelName)
		}
		slashEphemeral(w, fmt.Sprintf("✅ %s에 공지 메시지가 게시되었습니다.", chDesc))
		return
	}
	slashResponse(w, announceMsg)
}

// ── 도움말 슬래시 커맨드 ──────────────────────────────────────────────
// 사용법: /help
func handleHelp(w http.ResponseWriter) {
	msg := "### 🤖 DevBot 명령어 가이드\n\n---\n\n" +
		"#### 🖥 서버 관리\n" +
		"| 명령어 | 설명 | 예시 |\n|---|---|---|\n" +
		"| `/serverstatus` | 컨테이너·디스크·메모리 상태 | `/serverstatus` |\n" +
		"| `/log [컨테이너] [줄수]` | 컨테이너 로그 조회 | `/log mattermost 100` |\n" +
		"| `/deploy [대상]` | 서버 배포 (버튼 확인) | `/deploy staging` |\n\n---\n\n" +
		"#### 📅 예약 메시지\n" +
		"| 명령어 | 설명 | 예시 |\n|---|---|---|\n" +
		"| `/schedule HH:MM 메시지` | 현재 채널에 예약 전송 | `/schedule 09:00 점검 시작됩니다` |\n" +
		"| `/schedule list` | 예약 목록 확인 | `/schedule list` |\n" +
		"| `/schedule cancel ID` | 예약 취소 | `/schedule cancel 3` |\n\n---\n\n" +
		"#### 📢 공지\n" +
		"| 명령어 | 설명 | 예시 |\n|---|---|---|\n" +
		"| `/announce 메시지` | 현재 채널에 즉시 공지 | `/announce 공지 내용` |\n" +
		"| `/announce ~채널 메시지` | 특정 채널에 즉시 공지 | `/announce ~공지 내용` |\n" +
		"| `/announce HH:MM 메시지` | 현재 채널에 예약 공지 | `/announce 14:00 공지 내용` |\n" +
		"| `/announce ~채널 HH:MM 메시지` | 특정 채널에 예약 공지 | `/announce ~공지 14:00 내용` |\n\n---\n\n" +
		"#### 🌤 날씨\n" +
		"| 명령어 | 설명 | 예시 |\n|---|---|---|\n" +
		"| `/weather [도시]` | 기상청 현재 날씨 (기본: 서울) | `/weather 부산` |\n\n---\n\n" +
		"> 💡 **팁:** 메시지 입력창에 `/` 만 입력하면 전체 명령어 자동완성 목록이 표시됩니다."

	slashEphemeral(w, msg)
}
