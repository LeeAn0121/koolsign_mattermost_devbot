package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ScheduledMsg struct {
	ID         int
	ChannelID  string
	Message    string
	At         time.Time
	CreatedBy  string
	IsAnnounce bool // true면 공지 형식으로 전송
}

var (
	schedMu      sync.Mutex
	schedList    []ScheduledMsg
	schedCounter int
)

func addSchedule(channelID, createdBy, msg string, at time.Time, isAnnounce bool) int {
	schedMu.Lock()
	defer schedMu.Unlock()
	schedCounter++
	schedList = append(schedList, ScheduledMsg{
		ID:         schedCounter,
		ChannelID:  channelID,
		Message:    msg,
		At:         at,
		CreatedBy:  createdBy,
		IsAnnounce: isAnnounce,
	})
	return schedCounter
}

func listSchedules() []ScheduledMsg {
	schedMu.Lock()
	defer schedMu.Unlock()
	result := make([]ScheduledMsg, len(schedList))
	copy(result, schedList)
	return result
}

func cancelSchedule(id int) bool {
	schedMu.Lock()
	defer schedMu.Unlock()
	for i, s := range schedList {
		if s.ID == id {
			schedList = append(schedList[:i], schedList[i+1:]...)
			return true
		}
	}
	return false
}

// parseScheduleTime은 "HH:MM" 문자열을 오늘 날짜의 time.Time으로 변환합니다.
// 이미 지난 시간이면 내일로 설정합니다.
func parseScheduleTime(timeStr string) (time.Time, error) {
	parts := strings.Split(timeStr, ":")
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("HH:MM 형식이어야 합니다")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return time.Time{}, fmt.Errorf("시(Hour)는 0~23 사이여야 합니다")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return time.Time{}, fmt.Errorf("분(Minute)은 0~59 사이여야 합니다")
	}

	now := time.Now()
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())

	// 이미 지난 시간이거나 1분 미만으로 남은 경우 → 내일로 설정
	if !at.After(now.Add(1 * time.Minute)) {
		at = at.Add(24 * time.Hour)
	}
	return at, nil
}

func startScheduler() {
	log.Println("[Scheduler] 예약 메시지 스케줄러 시작 (30초 간격 체크)")
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		now := time.Now()
		schedMu.Lock()
		var remaining []ScheduledMsg
		for _, s := range schedList {
			if !now.Before(s.At) {
				log.Printf("[Scheduler] 예약 메시지 전송: ID=%d, Channel=%s, By=%s, Announce=%v", s.ID, s.ChannelID, s.CreatedBy, s.IsAnnounce)
				var msg string
				if s.IsAnnounce {
					msg = buildAnnounceMsg(s.Message, s.CreatedBy)
				} else {
					msg = fmt.Sprintf("📢 **[예약 메시지]**\n%s\n\n*예약자: @%s*", s.Message, s.CreatedBy)
				}
				go sendBotMessage(s.ChannelID, msg)
			} else {
				remaining = append(remaining, s)
			}
		}
		schedList = remaining
		schedMu.Unlock()
	}
}
