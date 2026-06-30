package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

func handleWeatherPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(weatherPageHTML))
}

func handleWeatherAPI(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if q == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "주소를 입력하세요"})
		return
	}

	data, err := fetchWeatherData(q)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(data)
}

const weatherPageHTML = `<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>날씨 조회 — DevBot</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,'Malgun Gothic','Segoe UI',sans-serif;background:#f0f4f8;color:#1a202c;min-height:100vh}
.wrap{max-width:760px;margin:0 auto;padding:32px 16px}
h1{font-size:1.4rem;font-weight:700;margin-bottom:24px;color:#2d3748}
.search-row{display:flex;gap:8px;margin-bottom:28px}
.search-row input{flex:1;padding:10px 14px;border:1.5px solid #cbd5e0;border-radius:10px;font-size:1rem;outline:none;transition:border .15s}
.search-row input:focus{border-color:#4299e1}
.search-row button{padding:10px 22px;background:#4299e1;color:#fff;border:none;border-radius:10px;cursor:pointer;font-size:1rem;font-weight:600;white-space:nowrap}
.search-row button:hover{background:#2b6cb0}
.regions{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:16px;margin-bottom:28px}
.region{background:#fff;border-radius:12px;padding:14px;box-shadow:0 1px 4px rgba(0,0,0,.06)}
.region h3{font-size:.75rem;font-weight:700;color:#718096;text-transform:uppercase;letter-spacing:.06em;margin-bottom:10px}
.cities{display:flex;flex-wrap:wrap;gap:6px}
.cb{padding:5px 12px;border:1.5px solid #e2e8f0;border-radius:20px;background:#fff;cursor:pointer;font-size:.85rem;color:#2d3748;transition:all .15s}
.cb:hover,.cb.active{background:#4299e1;color:#fff;border-color:#4299e1}
.result-box{background:#fff;border-radius:14px;padding:24px;box-shadow:0 2px 8px rgba(0,0,0,.08);display:none}
.weather-card{display:grid;grid-template-columns:1fr 1fr;gap:0}
.weather-header{grid-column:1/-1;margin-bottom:16px}
.weather-header .city{font-size:1.3rem;font-weight:700;color:#2d3748}
.weather-header .time{font-size:.8rem;color:#a0aec0;margin-top:2px}
.weather-main{grid-column:1/-1;display:flex;align-items:center;gap:20px;margin-bottom:20px;padding-bottom:20px;border-bottom:1px solid #edf2f7}
.weather-main .temp{font-size:3rem;font-weight:700;color:#2d3748}
.weather-main .desc{font-size:1.1rem;color:#4a5568}
.stat{padding:12px 0}
.stat:not(:last-child){border-bottom:1px solid #edf2f7}
.stat-label{font-size:.75rem;color:#a0aec0;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:2px}
.stat-value{font-size:.95rem;font-weight:600;color:#2d3748}
.error-msg{color:#e53e3e;font-size:.95rem}
.loading{color:#a0aec0;font-style:italic}
@media(max-width:500px){.weather-card{grid-template-columns:1fr}.weather-main .temp{font-size:2.4rem}}
</style>
</head>
<body>
<div class="wrap">
  <h1>날씨 조회</h1>

  <div class="search-row">
    <input type="text" id="addr" placeholder="주소 입력 (예: 서울시 강남구, 부산시 해운대구)" onkeydown="if(event.key==='Enter')doSearch()">
    <button onclick="doSearch()">조회</button>
  </div>

  <div class="regions">
    <div class="region">
      <h3>수도권</h3>
      <div class="cities">
        <button class="cb" onclick="quick(this,'서울')">서울</button>
        <button class="cb" onclick="quick(this,'인천')">인천</button>
        <button class="cb" onclick="quick(this,'수원')">수원</button>
        <button class="cb" onclick="quick(this,'성남')">성남</button>
        <button class="cb" onclick="quick(this,'의정부')">의정부</button>
      </div>
    </div>
    <div class="region">
      <h3>강원</h3>
      <div class="cities">
        <button class="cb" onclick="quick(this,'춘천')">춘천</button>
        <button class="cb" onclick="quick(this,'원주')">원주</button>
        <button class="cb" onclick="quick(this,'강릉')">강릉</button>
      </div>
    </div>
    <div class="region">
      <h3>충청</h3>
      <div class="cities">
        <button class="cb" onclick="quick(this,'대전')">대전</button>
        <button class="cb" onclick="quick(this,'세종')">세종</button>
        <button class="cb" onclick="quick(this,'청주')">청주</button>
        <button class="cb" onclick="quick(this,'천안')">천안</button>
      </div>
    </div>
    <div class="region">
      <h3>전라</h3>
      <div class="cities">
        <button class="cb" onclick="quick(this,'전주')">전주</button>
        <button class="cb" onclick="quick(this,'군산')">군산</button>
        <button class="cb" onclick="quick(this,'광주')">광주</button>
        <button class="cb" onclick="quick(this,'목포')">목포</button>
        <button class="cb" onclick="quick(this,'여수')">여수</button>
        <button class="cb" onclick="quick(this,'순천')">순천</button>
      </div>
    </div>
    <div class="region">
      <h3>경상</h3>
      <div class="cities">
        <button class="cb" onclick="quick(this,'대구')">대구</button>
        <button class="cb" onclick="quick(this,'포항')">포항</button>
        <button class="cb" onclick="quick(this,'경주')">경주</button>
        <button class="cb" onclick="quick(this,'안동')">안동</button>
        <button class="cb" onclick="quick(this,'구미')">구미</button>
        <button class="cb" onclick="quick(this,'울산')">울산</button>
        <button class="cb" onclick="quick(this,'부산')">부산</button>
        <button class="cb" onclick="quick(this,'창원')">창원</button>
        <button class="cb" onclick="quick(this,'진주')">진주</button>
      </div>
    </div>
    <div class="region">
      <h3>제주</h3>
      <div class="cities">
        <button class="cb" onclick="quick(this,'제주')">제주</button>
        <button class="cb" onclick="quick(this,'서귀포')">서귀포</button>
      </div>
    </div>
  </div>

  <div id="result" class="result-box"></div>
</div>

<script>
let activeBtn = null;

function quick(btn, city) {
  if (activeBtn) activeBtn.classList.remove('active');
  btn.classList.add('active');
  activeBtn = btn;
  document.getElementById('addr').value = city;
  fetch('/weather/api?q=' + encodeURIComponent(city)).then(r=>r.json()).then(render).catch(showErr);
  showLoading();
}

function doSearch() {
  const q = document.getElementById('addr').value.trim();
  if (!q) return;
  if (activeBtn) { activeBtn.classList.remove('active'); activeBtn = null; }
  showLoading();
  fetch('/weather/api?q=' + encodeURIComponent(q)).then(r=>r.json()).then(render).catch(showErr);
}

function showLoading() {
  const r = document.getElementById('result');
  r.style.display = 'block';
  r.innerHTML = '<span class="loading">날씨 조회 중...</span>';
}

function showErr() {
  document.getElementById('result').innerHTML = '<span class="error-msg">서버 연결 실패</span>';
}

function render(d) {
  const r = document.getElementById('result');
  if (d.error) {
    r.innerHTML = '<span class="error-msg">' + esc(d.error) + '</span>';
    return;
  }
  const date = d.baseDate ? d.baseDate.slice(0,4)+'-'+d.baseDate.slice(4,6)+'-'+d.baseDate.slice(6) : '';
  const time = d.baseTime ? d.baseTime.slice(0,2)+':'+d.baseTime.slice(2) : '';
  r.innerHTML = ` + "`" + `
    <div class="weather-header">
      <div class="city">${esc(d.city)}</div>
      <div class="time">기준 시각: ${date} ${time}</div>
    </div>
    <div class="weather-main">
      <div class="temp">${esc(d.temp)}°C</div>
      <div class="desc">${esc(d.weather)}</div>
    </div>
    <div class="weather-card">
      <div class="stat"><div class="stat-label">습도</div><div class="stat-value">${esc(d.humidity)}%</div></div>
      <div class="stat"><div class="stat-label">풍속</div><div class="stat-value">${esc(d.windSpeed)}</div></div>
      <div class="stat"><div class="stat-label">1시간 강수량</div><div class="stat-value">${esc(d.rain)}</div></div>
    </div>
  ` + "`" + `;
}

function esc(s) {
  if (!s) return '-';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}
</script>
</body>
</html>`
