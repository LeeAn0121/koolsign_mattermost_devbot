package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	memAlerted  bool
	httpAlerted = map[string]bool{}
)

// startMemoryMonitor 는 주기적으로 메모리 사용률을 체크하고 임계치 초과 시 알림을 전송합니다.
// 환경변수:
//
//	MEM_MONITOR_INTERVAL — 체크 주기 (분, 기본: 5)
//	MEM_THRESHOLD        — 경고 임계치 (%, 기본: 90)
//	MM_WEBHOOK_ALERT     — 알림 채널 Webhook URL
func startMemoryMonitor() {
	interval := 5
	if v, err := strconv.Atoi(getEnv("MEM_MONITOR_INTERVAL", "5")); err == nil && v > 0 {
		interval = v
	}
	threshold := 90
	if v, err := strconv.Atoi(getEnv("MEM_THRESHOLD", "90")); err == nil && v > 0 {
		threshold = v
	}
	webhookURL := getEnv("MM_WEBHOOK_ALERT", "")

	log.Printf("[SysMon] 메모리 모니터링 시작 (%d분 간격, 임계치: %d%%)", interval, threshold)

	ticker := time.NewTicker(time.Duration(interval) * time.Minute)
	for range ticker.C {
		pct, err := getMemUsagePct()
		if err != nil {
			log.Printf("[SysMon] 메모리 체크 실패: %v", err)
			continue
		}
		log.Printf("[SysMon] 메모리 사용률: %d%%", pct)
		if pct >= threshold && !memAlerted {
			memAlerted = true
			sendMM(webhookURL, fmt.Sprintf(
				"### [경고] 메모리 사용률 임계치 초과\n현재: **%d%%** (임계치: %d%%)\n조치가 필요합니다!", pct, threshold,
			))
		} else if pct < threshold-5 {
			memAlerted = false
		}
	}
}

func getMemUsagePct() (int, error) {
	out, err := runCmd("sh", "-c", "free -m | awk 'NR==2{printf \"%d\", $3*100/$2}'")
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return 0, fmt.Errorf("빈 응답")
	}
	return strconv.Atoi(s)
}

// startSSLChecker 는 매일 SSL 인증서 만료 일수를 체크하고 경고를 전송합니다.
// 환경변수:
//
//	SSL_CHECK_DOMAINS — 쉼표 구분 도메인 목록 (예: example.com,api.example.com)
//	SSL_WARN_DAYS     — 경고 임계치 (일, 기본: 14)
//	MM_WEBHOOK_ALERT  — 알림 채널 Webhook URL
func startSSLChecker() {
	domains := getEnv("SSL_CHECK_DOMAINS", "")
	if domains == "" {
		return
	}
	warnDays := 14
	if v, err := strconv.Atoi(getEnv("SSL_WARN_DAYS", "14")); err == nil && v > 0 {
		warnDays = v
	}
	webhookURL := getEnv("MM_WEBHOOK_ALERT", "")

	domainList := strings.Split(domains, ",")
	for i, d := range domainList {
		domainList[i] = strings.TrimSpace(d)
	}

	log.Printf("[SSL] 인증서 만료 체크 시작 — %d개 도메인, 경고: %d일 전", len(domainList), warnDays)

	check := func() {
		for _, domain := range domainList {
			if domain == "" {
				continue
			}
			days, err := sslDaysLeft(domain)
			if err != nil {
				log.Printf("[SSL] %s 체크 실패: %v", domain, err)
				continue
			}
			log.Printf("[SSL] %s — 만료까지 %d일", domain, days)
			if days <= warnDays {
				sendMM(webhookURL, fmt.Sprintf(
					"### [경고] SSL 인증서 만료 임박\n**도메인:** `%s`\n**만료까지:** **%d일**\n인증서 갱신이 필요합니다!",
					domain, days,
				))
			}
		}
	}

	check()
	ticker := time.NewTicker(24 * time.Hour)
	for range ticker.C {
		check()
	}
}

func sslDaysLeft(domain string) (int, error) {
	conn, err := tls.Dial("tcp", domain+":443", &tls.Config{ServerName: domain})
	if err != nil {
		return -1, err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return -1, fmt.Errorf("인증서 없음")
	}
	return int(time.Until(certs[0].NotAfter).Hours() / 24), nil
}

// startHTTPHealthCheck 는 주기적으로 HTTP 엔드포인트 상태를 확인하고 다운 시 알림을 전송합니다.
// 환경변수:
//
//	HEALTH_CHECK_URLS     — 쉼표 구분 URL 목록 (예: https://app.com/health,https://api.com/ping)
//	HEALTH_CHECK_INTERVAL — 체크 주기 (분, 기본: 1)
//	MM_WEBHOOK_ALERT      — 알림 채널 Webhook URL
func startHTTPHealthCheck() {
	urls := getEnv("HEALTH_CHECK_URLS", "")
	if urls == "" {
		return
	}
	interval := 1
	if v, err := strconv.Atoi(getEnv("HEALTH_CHECK_INTERVAL", "1")); err == nil && v > 0 {
		interval = v
	}
	webhookURL := getEnv("MM_WEBHOOK_ALERT", "")

	urlList := strings.Split(urls, ",")
	for i, u := range urlList {
		urlList[i] = strings.TrimSpace(u)
	}

	log.Printf("[HealthCheck] HTTP 헬스체크 시작 — %d개 URL, 간격: %d분", len(urlList), interval)

	client := &http.Client{Timeout: 10 * time.Second}

	ticker := time.NewTicker(time.Duration(interval) * time.Minute)
	for range ticker.C {
		for _, rawURL := range urlList {
			if rawURL == "" {
				continue
			}
			resp, err := client.Get(rawURL)
			isDown := err != nil || resp.StatusCode >= 500
			if resp != nil {
				resp.Body.Close()
			}

			if isDown && !httpAlerted[rawURL] {
				httpAlerted[rawURL] = true
				reason := "연결 실패"
				if err == nil {
					reason = fmt.Sprintf("HTTP %d", resp.StatusCode)
				}
				sendMM(webhookURL, fmt.Sprintf(
					"### [경고] 엔드포인트 다운\n**URL:** `%s`\n**원인:** %s", rawURL, reason,
				))
				log.Printf("[HealthCheck] 다운 감지: %s — %s", rawURL, reason)
			} else if !isDown && httpAlerted[rawURL] {
				delete(httpAlerted, rawURL)
				sendMM(webhookURL, fmt.Sprintf("### [복구] 엔드포인트 정상화\n**URL:** `%s`", rawURL))
				log.Printf("[HealthCheck] 복구 감지: %s", rawURL)
			}
		}
	}
}

// startDBBackup 는 매일 BACKUP_SCHEDULE_TIME 에 DB 백업 스크립트를 실행합니다.
// 환경변수:
//
//	BACKUP_SCHEDULE_TIME — HH:MM 형식 (미설정 시 비활성화)
//	BACKUP_SCRIPT        — 스크립트 경로 (기본: ./scripts/backup-db.sh)
//	MM_WEBHOOK_ALERT     — 결과 알림 Webhook URL
func startDBBackup() {
	schedTime := getEnv("BACKUP_SCHEDULE_TIME", "")
	if schedTime == "" {
		return
	}
	webhookURL := getEnv("MM_WEBHOOK_ALERT", "")
	scriptPath := getEnv("BACKUP_SCRIPT", "./scripts/backup-db.sh")

	log.Printf("[Backup] DB 백업 스케줄러 시작 — 매일 %s, 스크립트: %s", schedTime, scriptPath)

	for {
		next, err := parseScheduleTime(schedTime)
		if err != nil {
			log.Printf("[Backup] BACKUP_SCHEDULE_TIME 형식 오류: %v", err)
			return
		}
		time.Sleep(time.Until(next))

		log.Printf("[Backup] DB 백업 시작: %s", scriptPath)
		out, err := runScript(scriptPath)
		if err != nil {
			sendMM(webhookURL, fmt.Sprintf(
				"### [오류] DB 백업 실패\n**스크립트:** `%s`\n```\n%s\n```",
				scriptPath, truncate(string(out), 1000),
			))
			log.Printf("[Backup] 백업 실패: %v", err)
		} else {
			sendMM(webhookURL, fmt.Sprintf(
				"### [완료] DB 백업 성공\n**스크립트:** `%s`\n```\n%s\n```",
				scriptPath, truncate(string(out), 500),
			))
			log.Printf("[Backup] 백업 완료")
		}
	}
}
