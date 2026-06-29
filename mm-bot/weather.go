package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 기상청 격자 좌표 (nx, ny) — 주요 도시
var cityGrid = map[string][2]int{
	"서울":  {60, 127},
	"인천":  {55, 124},
	"수원":  {60, 121},
	"성남":  {62, 123},
	"의정부": {61, 131},
	"춘천":  {73, 134},
	"원주":  {76, 122},
	"강릉":  {92, 131},
	"대전":  {67, 100},
	"세종":  {66, 103},
	"청주":  {69, 106},
	"천안":  {63, 110},
	"전주":  {63, 89},
	"군산":  {56, 92},
	"광주":  {58, 74},
	"목포":  {50, 67},
	"여수":  {73, 66},
	"순천":  {70, 70},
	"대구":  {89, 90},
	"포항":  {102, 94},
	"경주":  {100, 91},
	"안동":  {91, 106},
	"구미":  {84, 96},
	"울산":  {102, 84},
	"부산":  {98, 76},
	"창원":  {90, 77},
	"진주":  {81, 75},
	"제주":  {52, 38},
	"서귀포": {52, 33},
}

// 기상청 API 응답 구조체
type kmaResp struct {
	Response struct {
		Header struct {
			ResultCode string `json:"resultCode"`
			ResultMsg  string `json:"resultMsg"`
		} `json:"header"`
		Body struct {
			Items struct {
				Item []struct {
					Category  string `json:"category"`
					ObsrValue string `json:"obsrValue"`
				} `json:"item"`
			} `json:"items"`
		} `json:"body"`
	} `json:"response"`
}

// 강수형태 코드
var ptyDesc = map[string]string{
	"0": "맑음 / 흐림",
	"1": "🌧 비",
	"2": "🌨 비/눈",
	"3": "❄️ 눈",
	"5": "🌦 빗방울",
	"6": "🌨 빗방울/눈날림",
	"7": "❄️ 눈날림",
}

// 풍향 각도 → 방향 문자열
func windDir(vecStr string) string {
	deg, err := strconv.ParseFloat(strings.TrimSpace(vecStr), 64)
	if err != nil {
		return ""
	}
	dirs := []string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE",
		"S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
	return dirs[int((deg+11.25)/22.5)%16]
}

// getWeather 는 기상청 API Hub 초단기실황 API를 통해 현재 날씨를 조회합니다.
// 환경변수: KMA_API_KEY (기상청 API Hub 인증키)
// 도시명이 비어 있으면 WEATHER_DEFAULT_CITY(기본값: 서울)를 사용합니다.
func getWeather(city string) (string, error) {
	apiKey := os.Getenv("KMA_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("KMA_API_KEY 환경변수가 설정되지 않았습니다\n발급: https://apihub.kma.go.kr")
	}
	if city == "" {
		city = getEnv("WEATHER_DEFAULT_CITY", "서울")
	}

	grid, ok := cityGrid[city]
	if !ok {
		// 지원 도시 목록 정렬해서 표시
		names := make([]string, 0, len(cityGrid))
		for k := range cityGrid {
			names = append(names, k)
		}
		sort.Strings(names)
		return "", fmt.Errorf("지원하지 않는 도시: `%s`\n**지원 도시:** %s", city, strings.Join(names, ", "))
	}

	// base_date, base_time 계산
	// 초단기실황은 매 시각 40분 이후에 해당 시각 데이터가 발표됨
	// 안전하게 현재 시각에서 1시간을 뺀 시각 사용
	now := time.Now()
	baseTime := now.Add(-1 * time.Hour)
	baseDateStr := baseTime.Format("20060102")
	baseTimeStr := fmt.Sprintf("%02d00", baseTime.Hour())

	apiURL := fmt.Sprintf(
		"https://apihub.kma.go.kr/api/typ02/openApi/VilageFcstInfoService_2.0/getUltraSrtNcst"+
			"?pageNo=1&numOfRows=10&dataType=JSON"+
			"&base_date=%s&base_time=%s&nx=%d&ny=%d&authKey=%s",
		baseDateStr, baseTimeStr, grid[0], grid[1], apiKey,
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("기상청 API 요청 실패: %v", err)
	}
	defer resp.Body.Close()

	var r kmaResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("응답 파싱 실패: %v", err)
	}

	if r.Response.Header.ResultCode != "00" {
		return "", fmt.Errorf("기상청 API 오류 [%s]: %s",
			r.Response.Header.ResultCode, r.Response.Header.ResultMsg)
	}

	// 카테고리별 값 추출
	data := map[string]string{}
	for _, item := range r.Response.Body.Items.Item {
		data[item.Category] = item.ObsrValue
	}

	temp := data["T1H"]     // 기온
	humidity := data["REH"] // 습도
	wsd := data["WSD"]      // 풍속
	vec := data["VEC"]      // 풍향
	rn1 := data["RN1"]      // 1시간 강수량

	pty := ptyDesc[data["PTY"]]
	if pty == "" {
		pty = "맑음 / 흐림"
	}

	windDirStr := windDir(vec)
	windInfo := wsd + " m/s"
	if windDirStr != "" {
		windInfo = fmt.Sprintf("%s m/s (%s)", wsd, windDirStr)
	}

	rainInfo := rn1 + " mm"
	if rn1 == "0" || rn1 == "" {
		rainInfo = "-"
	}

	return fmt.Sprintf(
		"### 🌡 %s 현재 날씨 (기상청)\n"+
			"| 항목 | 값 |\n|---|---|\n"+
			"| 날씨 | %s |\n"+
			"| 기온 | **%s°C** |\n"+
			"| 습도 | %s%% |\n"+
			"| 풍속 | %s |\n"+
			"| 1시간 강수량 | %s |\n"+
			"\n*기준 시각: %s %s*",
		city,
		pty,
		temp,
		humidity,
		windInfo,
		rainInfo,
		baseDateStr, baseTimeStr,
	), nil
}
