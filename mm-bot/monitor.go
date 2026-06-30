package main

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

var (
	dockerAlerted = map[string]bool{}
	dockerSeenUp  = map[string]bool{} // 한번이라도 Up 확인된 컨테이너만 모니터링
)

// startWeatherScheduler 는 매일 WEATHER_SCHEDULE_TIME 에 날씨를 자동 전송합니다.
// 환경변수:
//
//	WEATHER_SCHEDULE_TIME    — HH:MM 형식 (미설정 시 비활성화)
//	WEATHER_SCHEDULE_CITY    — 도시명 (기본: WEATHER_DEFAULT_CITY → 서울)
//	WEATHER_SCHEDULE_WEBHOOK — Mattermost Incoming Webhook URL
func startWeatherScheduler() {
	schedTime := getEnv("WEATHER_SCHEDULE_TIME", "")
	if schedTime == "" {
		return
	}
	webhookURL := getEnv("WEATHER_SCHEDULE_WEBHOOK", "")
	city := getEnv("WEATHER_SCHEDULE_CITY", getEnv("WEATHER_DEFAULT_CITY", "서울"))

	log.Printf("[Weather] 자동 날씨 브리핑 시작 — 매일 %s, 도시: %s", schedTime, city)

	for {
		next, err := parseScheduleTime(schedTime)
		if err != nil {
			log.Printf("[Weather] WEATHER_SCHEDULE_TIME 형식 오류: %v", err)
			return
		}
		time.Sleep(time.Until(next))

		msg, err := getWeather(city)
		if err != nil {
			log.Printf("[Weather] 자동 날씨 조회 실패: %v", err)
			continue
		}
		sendMM(webhookURL, "[자동 날씨 브리핑]\n"+msg)
	}
}

// startDockerMonitor 는 주기적으로 컨테이너 상태를 확인하고 비정상 시 알림을 전송합니다.
// 환경변수:
//
//	DOCKER_MONITOR_INTERVAL — 체크 주기 (분, 기본: 5)
//	MM_WEBHOOK_ALERT        — 알림 채널 Webhook URL
func startDockerMonitor() {
	intervalStr := getEnv("DOCKER_MONITOR_INTERVAL", "5")
	interval := 5
	if _, err := fmt.Sscanf(intervalStr, "%d", &interval); err != nil || interval <= 0 {
		interval = 5
	}
	webhookURL := getEnv("MM_WEBHOOK_ALERT", "")

	log.Printf("[Docker] 컨테이너 모니터링 시작 (%d분 간격)", interval)

	ticker := time.NewTicker(time.Duration(interval) * time.Minute)

	for range ticker.C {
		out, err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}\t{{.Status}}").Output()
		if err != nil {
			log.Printf("[Docker] docker ps 실패: %v", err)
			continue
		}

		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
			name, status := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			isUp := strings.HasPrefix(status, "Up")

			if isUp {
				dockerSeenUp[name] = true
				delete(dockerAlerted, name)
			} else if dockerSeenUp[name] && !dockerAlerted[name] {
				// 한번이라도 Up이었던 컨테이너만 alert
				dockerAlerted[name] = true
				msg := fmt.Sprintf("### [경고] 컨테이너 비정상\n**컨테이너:** `%s`\n**상태:** `%s`", name, status)
				sendMM(webhookURL, msg)
				log.Printf("[Docker] 비정상 알림: %s — %s", name, status)
			}
		}
	}
}
