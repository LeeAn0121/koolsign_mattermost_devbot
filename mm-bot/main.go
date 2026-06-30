package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func main() {
	// .env 파일 로드
	if err := godotenv.Load(); err != nil {
		log.Println("[Main] .env 파일을 찾을 수 없습니다. (환경변수가 이미 설정되어 있다고 가정합니다)")
	}

	loadDeployHistory()

	mux := http.NewServeMux()

	// ... 핸들러 등록 생략 ...
	mux.HandleFunc("/webhook/gitlab", handleGitlab)
	mux.HandleFunc("/webhook/gitlab/", handleGitlab)
	mux.HandleFunc("/webhook/gitea", handleGitea)
	mux.HandleFunc("/webhook/gitea/", handleGitea)
	mux.HandleFunc("/slash", handleSlash)
	mux.HandleFunc("/bot-action", handleBotAction)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/weather", handleWeatherPage)
	mux.HandleFunc("/weather/api", handleWeatherAPI)

	// 모든 요청을 기록하는 미들웨어
	loggingMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{w, http.StatusOK}
		mux.ServeHTTP(rw, r)
		log.Printf("[Access] %d | %s %s from %s (UA: %s)", rw.statusCode, r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
	})

	go startBot()
	go startHealthCheck()
	go startScheduler()
	go startWeatherScheduler()
	go startDockerMonitor()
	go startMemoryMonitor()
	go startSSLChecker()
	go startHTTPHealthCheck()
	go startDBBackup()
	go startMRReminderScheduler()

	log.Println("🚀 mm-bot 서버 시작 :9000")
	log.Fatal(http.ListenAndServe(":9000", loggingMux))
}

func startHealthCheck() {
	log.Println("[Health] 디스크 감시 시작 (1시간 간격)")
	ticker := time.NewTicker(1 * time.Hour)
	// 시작할 때 즉시 한 번 체크
	checkDisk()

	for range ticker.C {
		checkDisk()
	}
}

func checkDisk() {
	// df / | tail -1 | awk '{print $5}' | tr -d '%' 명령어로 사용량 퍼센트 숫자만 추출
	// Alpine/BusyBox df에서도 잘 작동하는 방식입니다.
	out, err := exec.Command("sh", "-c", "df / | tail -1 | awk '{print $5}' | tr -d '%'").Output()
	if err != nil {
		log.Printf("[Health] 디스크 체크 실패: %v", err)
		return
	}

	percentStr := strings.TrimSpace(string(out))
	if percentStr == "" {
		log.Printf("[Health] 디스크 체크 결과가 비어있습니다.")
		return
	}

	percent, err := strconv.Atoi(percentStr)
	if err != nil {
		log.Printf("[Health] 숫자 변환 실패: %v (원본데이터: [%s])", err, percentStr)
		return
	}

	log.Printf("[Health] 디스크 체크 완료: 현재 %d%% 사용 중", percent)

	if percent >= 90 {
		mmAlert := os.Getenv("MM_WEBHOOK_ALERT")
		msg := fmt.Sprintf("### ⚠️ [경고] 서버 디스크 용량 임계치 초과\n현재 루트(`/`) 디렉토리의 사용량이 **%d%%** 입니다. 조치가 필요합니다!", percent)
		sendMM(mmAlert, msg)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.Write([]byte("ok"))
}
