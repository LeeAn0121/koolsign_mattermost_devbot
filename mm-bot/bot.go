package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mattermost/mattermost-server/v6/model"
)

var mmClient *model.Client4
var botUser *model.User

func startBot() {
	serverURL := os.Getenv("MM_SERVER_URL")
	token := os.Getenv("MM_BOT_TOKEN")

	if token == "" {
		log.Println("[Bot] MM_BOT_TOKEN 미설정 — Bot 기능 비활성화")
		return
	}

	mmClient = model.NewAPIv4Client(serverURL)
	mmClient.SetToken(token)
	// API 클라이언트도 TLS 검증 건너뛰기 설정
	mmClient.HTTPClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	// 로그인 성공할 때까지 무한 재시도 (10초 간격)
	for {
		user, _, err := mmClient.GetMe("")
		if err == nil {
			botUser = user
			log.Printf("[Bot] 로그인 성공: @%s", botUser.Username)
			break
		}
		log.Printf("[Bot] 로그인 실패(서버가 아직 준비되지 않음): %v. 10초 후 재시도...", err)
		time.Sleep(10 * time.Second)
	}

	// 봇 상태를 '온라인'으로 설정
	mmClient.UpdateUserStatus(botUser.Id, &model.Status{UserId: botUser.Id, Status: "online"})

	// 웹소켓 URL 생성
	// Mattermost v6 클라이언트는 내부적으로 /api/v4/websocket을 붙이는 경우가 있으므로
	// 베이스 URL(ws://... 또는 wss://...)만 생성합니다.
	wsURL := strings.Replace(serverURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL = strings.TrimSuffix(wsURL, "/")

	log.Printf("[Bot] WebSocket 연결 시도: %s", wsURL)

	// 웹소켓 연결 및 리스닝 루프
	for {
		// TLS 검증 건너뛰기 및 Host 헤더 설정을 위한 다이얼러
		dialer := &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 45 * time.Second,
			TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
		}

		// NewWebSocketClient4는 내부적으로 주소 뒤에 /api/v4/websocket 을 붙입니다.
		ws, err := model.NewWebSocketClient4WithDialer(dialer, wsURL, token)
		if err != nil {
			log.Printf("[Bot] WebSocket 생성 실패 (10초 후 재시도): %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		if err := ws.Connect(); err != nil {
			log.Printf("[Bot] WebSocket 연결 에러: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		log.Println("[Bot] WebSocket 연결 성공! 대기 중...")
		ws.Listen()

		for event := range ws.EventChannel {
			if event == nil {
				continue
			}
			handleWebSocketEvent(event)
		}

		log.Println("[Bot] WebSocket 연결이 끊어졌습니다. 재연결 시도 중...")
		time.Sleep(5 * time.Second)
	}
}

func handleWebSocketEvent(event *model.WebSocketEvent) {
	if event.EventType() != model.WebsocketEventPosted {
		return
	}

	postData, ok := event.GetData()["post"].(string)
	if !ok {
		return
	}

	post := &model.Post{}
	if err := json.Unmarshal([]byte(postData), post); err != nil {
		return
	}

	if post.UserId == botUser.Id {
		return
	}

	msg := strings.TrimSpace(post.Message)
	mention := fmt.Sprintf("@%s", botUser.Username)

	// 멘션 포함 여부 확인 (대소문자 무시)
	if !strings.Contains(strings.ToLower(msg), strings.ToLower(mention)) {
		return
	}

	log.Printf("[Bot] 멘션 감지: [%s] %s", botUser.Username, msg)
	cmd := strings.TrimSpace(strings.ReplaceAll(msg, mention, ""))
	go handleBotCommand(post.ChannelId, post.UserId, cmd)
}

func handleBotCommand(channelID, userID, cmd string) {
	user, _, _ := mmClient.GetUser(userID, "")
	userName := "unknown"
	if user != nil {
		userName = user.Username
	}

	cmdLower := strings.ToLower(strings.TrimSpace(cmd))
	var reply string

	switch {
	case cmdLower == "도움말" || cmdLower == "help":
		reply = buildHelpMsg()

	case strings.HasPrefix(cmdLower, "로그 ") || strings.HasPrefix(cmdLower, "log "):
		args := strings.Fields(cmd)
		if len(args) < 2 {
			reply = "사용법: `@devbot 로그 [컨테이너명] [줄수]`"
			break
		}
		lines := "50"
		if len(args) >= 3 {
			lines = args[2]
		}
		out, err := runDockerLog(args[1], lines)
		if err != nil {
			reply = fmt.Sprintf("❌ %v", err)
		} else {
			reply = fmt.Sprintf("📋 **%s** 최근 %s줄\n```\n%s\n```", args[1], lines, out)
		}

	case cmdLower == "상태" || cmdLower == "status":
		ps, _ := runCmd("docker", "ps", "--format", "| {{.Names}} | {{.Status}} |")
		df, _ := runCmd("df", "-h", "/")
		mem, _ := runCmd("free", "-h")
		reply = fmt.Sprintf(
			"### 🖥 서버 상태\n\n**컨테이너**\n| 이름 | 상태 |\n|---|---|\n%s\n\n**디스크**\n```\n%s\n```\n**메모리**\n```\n%s\n```",
			ps, truncate(df, 400), truncate(mem, 400),
		)

	case cmdLower == "ps" || cmdLower == "컨테이너":
		out, _ := runCmd("docker", "ps", "--format", "| {{.Names}} | {{.Status}} | {{.Ports}} |")
		reply = fmt.Sprintf("### 🐳 실행 중인 컨테이너\n| 이름 | 상태 | 포트 |\n|---|---|---|\n%s", out)

	case strings.HasPrefix(cmdLower, "배포 ") || strings.HasPrefix(cmdLower, "deploy "):
		args := strings.Fields(cmd)
		if len(args) < 2 {
			reply = "사용법: `@devbot 배포 [staging|prod]`"
			break
		}
		sendDeployConfirm(channelID, args[1], userName)
		return

	// ── 예약 메시지 ──────────────────────────────────────────────
	case strings.HasPrefix(cmdLower, "예약 ") || strings.HasPrefix(cmdLower, "schedule "):
		// @devbot 예약 HH:MM 메시지내용
		// 첫 번째 공백 이후 인자를 파싱 (명령어 단어 제거)
		rest := strings.SplitN(strings.TrimSpace(cmd), " ", 3)
		if len(rest) < 3 {
			reply = "사용법: `@devbot 예약 HH:MM 메시지내용`\n예: `@devbot 예약 14:30 서버 점검 시작됩니다`"
			break
		}
		timeStr := rest[1]
		message := rest[2]
		at, err := parseScheduleTime(timeStr)
		if err != nil {
			reply = fmt.Sprintf("❌ 시간 형식 오류: `%s` → %v\n(HH:MM 형식 사용, 예: `09:00`)", timeStr, err)
			break
		}
		id := addSchedule(channelID, userName, message, at, false)
		reply = fmt.Sprintf("✅ 예약 완료! (ID: **%d**)\n- 전송 시간: **%s**\n- 메시지: %s", id, at.Format("01/02 15:04"), message)

	case cmdLower == "예약목록" || cmdLower == "예약 목록" || cmdLower == "schedules":
		list := listSchedules()
		if len(list) == 0 {
			reply = "📭 예약된 메시지가 없습니다."
			break
		}
		reply = "### 📅 예약된 메시지 목록\n| ID | 유형 | 전송시간 | 메시지 |\n|---|---|---|---|\n"
		for _, s := range list {
			kind := "일반"
			if s.IsAnnounce {
				kind = "📢 공지"
			}
			reply += fmt.Sprintf("| %d | %s | %s | %s |\n", s.ID, kind, s.At.Format("01/02 15:04"), truncate(s.Message, 50))
		}

	case cmdLower == "공지목록" || cmdLower == "공지 목록":
		list := listSchedules()
		var found []ScheduledMsg
		for _, s := range list {
			if s.IsAnnounce {
				found = append(found, s)
			}
		}
		if len(found) == 0 {
			reply = "예약된 공지가 없습니다."
			break
		}
		reply = "### 예약된 공지 목록\n| ID | 전송시간 | 예약자 | 내용 |\n|---|---|---|---|\n"
		for _, s := range found {
			reply += fmt.Sprintf("| %d | %s | @%s | %s |\n", s.ID, s.At.Format("01/02 15:04"), s.CreatedBy, truncate(s.Message, 40))
		}

	case strings.HasPrefix(cmdLower, "예약취소 ") || strings.HasPrefix(cmdLower, "예약 취소 "):
		args := strings.Fields(cmd)
		if len(args) < 2 {
			reply = "사용법: `@devbot 예약취소 [ID]`"
			break
		}
		id, err := strconv.Atoi(args[len(args)-1])
		if err != nil {
			reply = "❌ ID는 숫자여야 합니다. 예: `@devbot 예약취소 3`"
			break
		}
		if cancelSchedule(id) {
			reply = fmt.Sprintf("✅ 예약 메시지 ID **%d** 취소 완료", id)
		} else {
			reply = fmt.Sprintf("❌ ID **%d** 예약을 찾을 수 없습니다.", id)
		}

	// ── 날씨 확인 ────────────────────────────────────────────────
	case cmdLower == "날씨" || cmdLower == "weather" ||
		strings.HasPrefix(cmdLower, "날씨 ") || strings.HasPrefix(cmdLower, "weather "):
		parts := strings.Fields(cmd)
		city := ""
		if len(parts) >= 2 {
			city = strings.Join(parts[1:], " ")
		}
		weatherMsg, err := getWeather(city)
		if err != nil {
			reply = fmt.Sprintf("❌ %v", err)
		} else {
			reply = weatherMsg
		}

	// ── 공지 메시지 ──────────────────────────────────────────────
	// 사용법: @devbot 공지 메시지내용
	//         @devbot 공지 ~채널명 메시지내용
	//         @devbot 공지 HH:MM 메시지내용
	//         @devbot 공지 ~채널명 HH:MM 메시지내용
	case strings.HasPrefix(cmdLower, "공지 ") || strings.HasPrefix(cmdLower, "announce "):
		spaceIdx := strings.Index(cmd, " ")
		rest := strings.TrimSpace(cmd[spaceIdx+1:])
		if rest == "" {
			reply = "사용법:\n" +
				"- `@devbot 공지 메시지` — 즉시, 현재 채널\n" +
				"- `@devbot 공지 ~채널명 메시지` — 즉시, 특정 채널\n" +
				"- `@devbot 공지 HH:MM 메시지` — 예약, 현재 채널\n" +
				"- `@devbot 공지 ~채널명 HH:MM 메시지` — 예약, 특정 채널"
			break
		}

		channelName, schedAt, content, err := parseAnnounceArgs(rest)
		if err != nil {
			reply = fmt.Sprintf("❌ %v", err)
			break
		}

		// 채널 결정
		targetChannelID := channelID
		if channelName != "" {
			chID, cerr := resolveChannelID("", channelName)
			if cerr != nil {
				reply = fmt.Sprintf("❌ %v", cerr)
				break
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
			reply = fmt.Sprintf("✅ 공지 예약 완료! (ID: **%d**)\n- 채널: %s\n- 전송 시간: **%s**\n- 내용: %s",
				id, chDesc, schedAt.Format("01/02 15:04"), content)
			break
		}

		// 즉시 공지
		announceMsg := buildAnnounceMsg(content, userName)
		sendBotMessage(targetChannelID, announceMsg)
		if targetChannelID != channelID {
			sendBotMessage(channelID, fmt.Sprintf("✅ `~%s` 채널에 공지를 게시했습니다.", channelName))
		}
		return

	case strings.HasPrefix(cmdLower, "재시작 ") || strings.HasPrefix(cmdLower, "restart "):
		args := strings.Fields(cmd)
		if len(args) < 2 {
			reply = "사용법: `@devbot 재시작 [컨테이너명]`"
			break
		}
		sendRestartConfirm(channelID, args[1], userName)
		return

	case cmdLower == "업타임" || cmdLower == "uptime":
		out, _ := runCmd("docker", "ps", "--format", "| {{.Names}} | {{.Status}} | {{.RunningFor}} |")
		if strings.TrimSpace(out) == "" {
			reply = "실행 중인 컨테이너가 없습니다."
		} else {
			reply = "### 컨테이너 업타임\n| 이름 | 상태 | 실행 시간 |\n|---|---|---|\n" + out
		}

	case cmdLower == "이미지목록" || cmdLower == "이미지 목록" || cmdLower == "images":
		out, _ := runCmd("docker", "images", "--format", "| {{.Repository}} | {{.Tag}} | {{.Size}} | {{.CreatedSince}} |")
		if strings.TrimSpace(out) == "" {
			reply = "이미지가 없습니다."
		} else {
			reply = "### Docker 이미지 목록\n| 저장소 | 태그 | 크기 | 생성일 |\n|---|---|---|---|\n" + out
		}

	case strings.HasPrefix(cmdLower, "grep "):
		args := strings.Fields(cmd)
		if len(args) < 3 {
			reply = "사용법: `@devbot grep [컨테이너] [패턴]`\n예: `@devbot grep mattermost ERROR`"
			break
		}
		container := args[1]
		pattern := strings.ToLower(strings.Join(args[2:], " "))
		logsOut, err := runCmd("docker", "logs", "--tail", "1000", container)
		if err != nil {
			reply = fmt.Sprintf("컨테이너를 찾을 수 없습니다: `%s`", container)
			break
		}
		var matched []string
		for _, line := range strings.Split(logsOut, "\n") {
			if strings.Contains(strings.ToLower(line), pattern) {
				matched = append(matched, line)
			}
		}
		if len(matched) == 0 {
			reply = fmt.Sprintf("`%s` 로그에서 `%s` 패턴을 찾을 수 없습니다.", container, strings.Join(args[2:], " "))
			break
		}
		if len(matched) > 50 {
			matched = matched[len(matched)-50:]
		}
		reply = fmt.Sprintf("### `%s` 로그 검색: `%s` (%d건)\n```\n%s\n```",
			container, strings.Join(args[2:], " "), len(matched), truncate(strings.Join(matched, "\n"), 3000))

	case cmdLower == "히스토리" || cmdLower == "history" ||
		strings.HasPrefix(cmdLower, "히스토리 ") || strings.HasPrefix(cmdLower, "history "):
		n := 10
		parts := strings.Fields(cmd)
		if len(parts) >= 2 {
			if v, err := strconv.Atoi(parts[1]); err == nil && v > 0 {
				n = v
			}
		}
		reply = formatDeployHistory(n)

	case cmdLower == "대시보드" || cmdLower == "dashboard":
		reply = getDashboard()

	default:
		reply = fmt.Sprintf("@%s 이해할 수 없는 명령어입니다.\n`@devbot 도움말` 로 명령어 목록을 확인하세요.", userName)
	}

	if reply != "" {
		sendBotMessage(channelID, reply)
	}
}

func sendDeployConfirm(channelID, target, userName string) {
	allowed := map[string]bool{"staging": true, "prod": true}
	if !allowed[target] {
		sendBotMessage(channelID, fmt.Sprintf("❌ 알 수 없는 배포 대상: `%s`", target))
		return
	}

	emoji := "🔵"
	if target == "prod" {
		emoji = "🔴"
	}

	botActionURL := os.Getenv("BOT_ACTION_URL")
	post := &model.Post{
		ChannelId: channelID,
		Message:   fmt.Sprintf("%s @%s님이 **%s** 배포를 요청했습니다.", emoji, userName, target),
		Props: model.StringInterface{
			"attachments": []map[string]interface{}{
				{
					"actions": []map[string]interface{}{
						{
							"name":  "✅ 배포 진행",
							"type":  "button",
							"style": "success",
							"integration": map[string]interface{}{
								"url": botActionURL + "/bot-action",
								"context": map[string]interface{}{
									"action":      "deploy",
									"target":      target,
									"requestedBy": userName,
								},
							},
						},
						{
							"name":  "❌ 취소",
							"type":  "button",
							"style": "danger",
							"integration": map[string]interface{}{
								"url": botActionURL + "/bot-action",
								"context": map[string]interface{}{
									"action": "cancel",
								},
							},
						},
					},
				},
			},
		},
	}
	mmClient.CreatePost(post)
}

func sendRestartConfirm(channelID, container, userName string) {
	if mmClient == nil {
		return
	}
	botActionURL := os.Getenv("BOT_ACTION_URL")
	post := &model.Post{
		ChannelId: channelID,
		Message:   fmt.Sprintf("@%s님이 `%s` 컨테이너 재시작을 요청했습니다.", userName, container),
		Props: model.StringInterface{
			"attachments": []map[string]interface{}{
				{
					"actions": []map[string]interface{}{
						{
							"name":  "재시작",
							"type":  "button",
							"style": "danger",
							"integration": map[string]interface{}{
								"url": botActionURL + "/bot-action",
								"context": map[string]interface{}{
									"action":      "restart",
									"container":   container,
									"requestedBy": userName,
								},
							},
						},
						{
							"name":  "취소",
							"type":  "button",
							"style": "default",
							"integration": map[string]interface{}{
								"url": botActionURL + "/bot-action",
								"context": map[string]interface{}{
									"action": "cancel",
								},
							},
						},
					},
				},
			},
		},
	}
	mmClient.CreatePost(post)
}

func sendDMToUser(username, msg string) {
	if mmClient == nil || botUser == nil || username == "" {
		return
	}
	user, _, err := mmClient.GetUserByUsername(strings.ToLower(username), "")
	if err != nil || user == nil {
		log.Printf("[Bot] DM 대상 사용자 없음: %s", username)
		return
	}
	ch, _, err := mmClient.CreateDirectChannel(botUser.Id, user.Id)
	if err != nil || ch == nil {
		log.Printf("[Bot] DM 채널 생성 실패: %v", err)
		return
	}
	sendBotMessage(ch.Id, msg)
}

func handleBotAction(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var payload struct {
		Context map[string]interface{} `json:"context"`
		UserId  string                 `json:"user_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}

	user, _, _ := mmClient.GetUser(payload.UserId, "")
	userName := "unknown"
	if user != nil {
		userName = user.Username
	}

	action := fmt.Sprintf("%v", payload.Context["action"])

	w.Header().Set("Content-Type", "application/json")

	if action == "cancel" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"update": map[string]string{"message": "⏹ 배포가 취소되었습니다."},
		})
		return
	}

	if action == "deploy" {
		target := fmt.Sprintf("%v", payload.Context["target"])
		requestedBy := fmt.Sprintf("%v", payload.Context["requestedBy"])

		json.NewEncoder(w).Encode(map[string]interface{}{
			"update": map[string]string{
				"message": fmt.Sprintf("🚀 **%s** 배포 진행 중... (승인: @%s)", target, userName),
			},
		})

		go func() {
			mmDeploy := os.Getenv("MM_WEBHOOK_DEPLOY")
			mmAlert := os.Getenv("MM_WEBHOOK_ALERT")

			scripts := map[string]string{
				"staging": "./scripts/deploy-staging.sh",
				"prod":    "./scripts/deploy-prod.sh",
			}
			out, err := runScript(scripts[target])
			if err != nil {
				addDeployHistory(target, requestedBy, userName, "failed", string(out))
				errMsg := fmt.Sprintf("### ❌ 배포 실패 — `%s`\n요청: @%s | 승인: @%s\n```\n%s\n```",
					target, requestedBy, userName, truncate(string(out), 2000))
				sendMM(mmDeploy, errMsg)
				if target == "prod" {
					sendMM(mmAlert, "## 🚨 [CRITICAL] 프로덕션 배포 실패!\n즉시 확인이 필요합니다.\n"+errMsg)
				}
				return
			}
			addDeployHistory(target, requestedBy, userName, "success", string(out))
			sendMM(mmDeploy, fmt.Sprintf(
				"### ✅ 배포 완료 — `%s`\n요청: @%s | 승인: @%s\n```\n%s\n```",
				target, requestedBy, userName, truncate(string(out), 2000),
			))
		}()
		return
	}

	if action == "restart" {
		container := fmt.Sprintf("%v", payload.Context["container"])
		requestedBy := fmt.Sprintf("%v", payload.Context["requestedBy"])

		json.NewEncoder(w).Encode(map[string]interface{}{
			"update": map[string]string{
				"message": fmt.Sprintf("`%s` 재시작 중... (승인: @%s)", container, userName),
			},
		})

		go func() {
			mmAlert := os.Getenv("MM_WEBHOOK_ALERT")
			out, err := runCmd("docker", "restart", container)
			if err != nil {
				sendMM(mmAlert, fmt.Sprintf(
					"### 컨테이너 재시작 실패\n**컨테이너:** `%s`\n요청: @%s | 승인: @%s\n```\n%s\n```",
					container, requestedBy, userName, truncate(out, 500),
				))
			} else {
				sendMM(mmAlert, fmt.Sprintf(
					"### 컨테이너 재시작 완료\n**컨테이너:** `%s`\n요청: @%s | 승인: @%s",
					container, requestedBy, userName,
				))
			}
		}()
		return
	}

	w.WriteHeader(200)
}

func sendBotMessage(channelID, msg string) {
	if mmClient == nil {
		return
	}
	mmClient.CreatePost(&model.Post{ChannelId: channelID, Message: msg})
}

func buildHelpMsg() string {
	help := "### 🤖 DevBot 명령어 목록\n\n"
	help += "**서버 관리**\n```\n"
	help += "@devbot 상태                     - 서버/컨테이너/디스크/메모리 상태\n"
	help += "@devbot 컨테이너                 - 실행 중인 컨테이너 목록\n"
	help += "@devbot 업타임                   - 컨테이너별 실행 시간\n"
	help += "@devbot 이미지목록               - Docker 이미지 목록\n"
	help += "@devbot 로그 [컨테이너] [줄수]    - 컨테이너 로그 조회\n"
	help += "@devbot grep [컨테이너] [패턴]   - 컨테이너 로그 검색\n"
	help += "@devbot 재시작 [컨테이너]        - 컨테이너 재시작 (확인 버튼)\n"
	help += "@devbot 배포 staging             - 스테이징 배포 (확인 버튼 포함)\n"
	help += "@devbot 배포 prod                - 프로덕션 배포 (확인 버튼 포함)\n"
	help += "@devbot 히스토리 [건수]           - 배포 히스토리 (기본 10건)\n"
	help += "@devbot 대시보드                 - GitLab+Gitea 통합 현황\n"
	help += "```\n"
	help += "**예약 메시지**\n```\n"
	help += "@devbot 예약 HH:MM 메시지        - 지정 시간에 이 채널로 메시지 전송\n"
	help += "@devbot 예약목록                 - 예약된 메시지 목록\n"
	help += "@devbot 예약취소 [ID]            - 예약 메시지 취소\n"
	help += "```\n"
	help += "**날씨 / 공지**\n```\n"
	help += "@devbot 날씨 [도시명]            - 날씨 조회 (기본: 서울)\n"
	help += "@devbot 공지 메시지 내용         - 공지 형식으로 메시지 게시\n"
	help += "@devbot 공지목록                 - 예약된 공지 목록\n"
	help += "```\n"
	return help
}

func buildAnnounceMsg(content, author string) string {
	now := time.Now().Format("2006-01-02 15:04")
	return fmt.Sprintf(
		"---\n### 📢 공지사항\n\n%s\n\n---\n*작성: @%s | %s*",
		content, author, now,
	)
}
