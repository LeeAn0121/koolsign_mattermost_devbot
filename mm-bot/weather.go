package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
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

// WeatherData 는 날씨 조회 결과를 담는 구조체입니다.
type WeatherData struct {
	City      string `json:"city"`
	Weather   string `json:"weather"`
	Temp      string `json:"temp"`
	Humidity  string `json:"humidity"`
	WindSpeed string `json:"windSpeed"`
	WindDir   string `json:"windDir"`
	Rain      string `json:"rain"`
	BaseDate  string `json:"baseDate"`
	BaseTime  string `json:"baseTime"`
}

// 강수형태 코드
var ptyDesc = map[string]string{
	"0": "맑음 / 흐림",
	"1": "비",
	"2": "비/눈",
	"3": "눈",
	"5": "빗방울",
	"6": "빗방울/눈날림",
	"7": "눈날림",
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

// latLonToGrid 는 위경도를 기상청 격자 좌표로 변환합니다 (Lambert Conformal Conic).
func latLonToGrid(lat, lon float64) (int, int) {
	const (
		RE    = 6371.00877
		GRID  = 5.0
		SLAT1 = 30.0
		SLAT2 = 60.0
		OLON  = 126.0
		OLAT  = 38.0
		XO    = 43.0
		YO    = 136.0
	)
	deg := math.Pi / 180.0

	slat1 := SLAT1 * deg
	slat2 := SLAT2 * deg
	olon := OLON * deg
	olat := OLAT * deg
	re := RE / GRID

	sn := math.Log(math.Cos(slat1)/math.Cos(slat2)) /
		math.Log(math.Tan(math.Pi*0.25+slat2*0.5)/math.Tan(math.Pi*0.25+slat1*0.5))
	sf := math.Pow(math.Tan(math.Pi*0.25+slat1*0.5), sn) * math.Cos(slat1) / sn
	ro := re * sf / math.Pow(math.Tan(math.Pi*0.25+olat*0.5), sn)

	ra := re * sf / math.Pow(math.Tan(math.Pi*0.25+lat*deg*0.5), sn)
	theta := lon*deg - olon
	for theta > math.Pi {
		theta -= 2 * math.Pi
	}
	for theta < -math.Pi {
		theta += 2 * math.Pi
	}
	theta *= sn

	nx := int(ra*math.Sin(theta) + XO + 0.5)
	ny := int(ro - ra*math.Cos(theta) + YO + 0.5)
	return nx, ny
}

// geocodeAddress 는 Nominatim(OpenStreetMap)으로 주소를 위경도로 변환합니다.
func geocodeAddress(address string) (float64, float64, error) {
	apiURL := "https://nominatim.openstreetmap.org/search?q=" +
		url.QueryEscape(address) + "&format=json&limit=1&countrycodes=kr"
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "mm-bot/1.0 (+internal-tool)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	var results []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return 0, 0, err
	}
	if len(results) == 0 {
		return 0, 0, fmt.Errorf("주소를 찾을 수 없습니다: %s", address)
	}

	latF, _ := strconv.ParseFloat(results[0].Lat, 64)
	lonF, _ := strconv.ParseFloat(results[0].Lon, 64)
	return latF, lonF, nil
}

func supportedCities() string {
	names := make([]string, 0, len(cityGrid))
	for k := range cityGrid {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// fetchWeatherData 는 기상청 초단기실황 API를 호출해 WeatherData를 반환합니다.
// city 가 도시 목록에 없으면 Nominatim 으로 지오코딩합니다.
func fetchWeatherData(city string) (*WeatherData, error) {
	apiKey := os.Getenv("KMA_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("KMA_API_KEY 환경변수가 설정되지 않았습니다")
	}

	var nx, ny int
	if grid, ok := cityGrid[city]; ok {
		nx, ny = grid[0], grid[1]
	} else {
		lat, lon, err := geocodeAddress(city)
		if err != nil {
			return nil, fmt.Errorf("%v\n\n**지원 도시:** %s", err, supportedCities())
		}
		nx, ny = latLonToGrid(lat, lon)
	}

	now := time.Now()
	// KMA 초단기실황은 매시 :40에 해당 시각 데이터 제공. 40분 이후면 현재 시각 사용.
	base := now
	if now.Minute() < 40 {
		base = now.Add(-1 * time.Hour)
	}
	baseDateStr := base.Format("20060102")
	baseTimeStr := fmt.Sprintf("%02d00", base.Hour())

	apiURL := fmt.Sprintf(
		"https://apihub.kma.go.kr/api/typ02/openApi/VilageFcstInfoService_2.0/getUltraSrtNcst"+
			"?pageNo=1&numOfRows=10&dataType=JSON"+
			"&base_date=%s&base_time=%s&nx=%d&ny=%d&authKey=%s",
		baseDateStr, baseTimeStr, nx, ny, apiKey,
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("기상청 API 요청 실패: %v", err)
	}
	defer resp.Body.Close()

	var r kmaResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("응답 파싱 실패: %v", err)
	}
	if r.Response.Header.ResultCode != "00" {
		return nil, fmt.Errorf("기상청 API 오류 [%s]: %s",
			r.Response.Header.ResultCode, r.Response.Header.ResultMsg)
	}

	data := map[string]string{}
	for _, item := range r.Response.Body.Items.Item {
		data[item.Category] = item.ObsrValue
	}

	pty := ptyDesc[data["PTY"]]
	if pty == "" {
		pty = "맑음 / 흐림"
	}

	wdStr := windDir(data["VEC"])
	windSpeed := data["WSD"] + " m/s"
	if wdStr != "" {
		windSpeed = fmt.Sprintf("%s m/s (%s)", data["WSD"], wdStr)
	}

	rain := data["RN1"] + " mm"
	if data["RN1"] == "0" || data["RN1"] == "" {
		rain = "-"
	}

	return &WeatherData{
		City:      city,
		Weather:   pty,
		Temp:      data["T1H"],
		Humidity:  data["REH"],
		WindSpeed: windSpeed,
		WindDir:   wdStr,
		Rain:      rain,
		BaseDate:  baseDateStr,
		BaseTime:  baseTimeStr,
	}, nil
}

// getWeather 는 Mattermost 메시지용 날씨 문자열을 반환합니다.
func getWeather(city string) (string, error) {
	if city == "" {
		city = getEnv("WEATHER_DEFAULT_CITY", "서울")
	}

	d, err := fetchWeatherData(city)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"### %s 현재 날씨 (기상청)\n"+
			"| 항목 | 값 |\n|---|---|\n"+
			"| 날씨 | %s |\n"+
			"| 기온 | **%s°C** |\n"+
			"| 습도 | %s%% |\n"+
			"| 풍속 | %s |\n"+
			"| 1시간 강수량 | %s |\n"+
			"\n*기준 시각: %s %s*",
		d.City, d.Weather, d.Temp, d.Humidity, d.WindSpeed, d.Rain, d.BaseDate, d.BaseTime,
	), nil
}
