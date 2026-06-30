package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const historyFile = "./data/deploy-history.json"

type DeployRecord struct {
	ID          int
	Target      string
	RequestedBy string
	ApprovedBy  string
	Status      string
	Output      string
	At          time.Time
}

var (
	histMu      sync.Mutex
	deployHist  []DeployRecord
	histCounter int
)

func loadDeployHistory() {
	data, err := os.ReadFile(historyFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[History] 파일 로드 실패: %v", err)
		}
		return
	}
	var records []DeployRecord
	if err := json.Unmarshal(data, &records); err != nil {
		log.Printf("[History] JSON 파싱 실패: %v", err)
		return
	}
	histMu.Lock()
	defer histMu.Unlock()
	deployHist = records
	for _, r := range records {
		if r.ID > histCounter {
			histCounter = r.ID
		}
	}
	log.Printf("[History] 배포 기록 %d건 로드", len(records))
}

func saveDeployHistory() {
	if err := os.MkdirAll("./data", 0755); err != nil {
		log.Printf("[History] data 디렉토리 생성 실패: %v", err)
		return
	}
	data, err := json.Marshal(deployHist)
	if err != nil {
		log.Printf("[History] JSON 직렬화 실패: %v", err)
		return
	}
	if err := os.WriteFile(historyFile, data, 0644); err != nil {
		log.Printf("[History] 파일 저장 실패: %v", err)
	}
}

func addDeployHistory(target, requestedBy, approvedBy, status, output string) {
	histMu.Lock()
	defer histMu.Unlock()
	histCounter++
	deployHist = append(deployHist, DeployRecord{
		ID:          histCounter,
		Target:      target,
		RequestedBy: requestedBy,
		ApprovedBy:  approvedBy,
		Status:      status,
		Output:      output,
		At:          time.Now(),
	})
	if len(deployHist) > 100 {
		deployHist = deployHist[len(deployHist)-100:]
	}
	saveDeployHistory()
}

func listDeployHistory(n int) []DeployRecord {
	histMu.Lock()
	defer histMu.Unlock()
	if len(deployHist) == 0 {
		return nil
	}
	if n <= 0 || n > len(deployHist) {
		n = len(deployHist)
	}
	result := make([]DeployRecord, n)
	copy(result, deployHist[len(deployHist)-n:])
	return result
}

func formatDeployHistory(n int) string {
	records := listDeployHistory(n)
	if len(records) == 0 {
		return "배포 기록이 없습니다."
	}
	var sb strings.Builder
	sb.WriteString("### 배포 히스토리\n| ID | 대상 | 상태 | 요청자 | 승인자 | 시각 |\n|---|---|---|---|---|---|\n")
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		fmt.Fprintf(&sb, "| %d | `%s` | %s | @%s | @%s | %s |\n",
			r.ID, r.Target, r.Status, r.RequestedBy, r.ApprovedBy, r.At.Format("01/02 15:04"))
	}
	return sb.String()
}
