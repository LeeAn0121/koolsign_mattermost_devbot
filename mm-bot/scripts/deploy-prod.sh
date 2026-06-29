#!/bin/sh
set -e

TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')
echo "[$TIMESTAMP] 프로덕션 배포 시작"

# 대상 컨테이너 (프로덕션)
TARGET_IMAGE="mattermost/mattermost-team-edition:latest"

echo "[$(date '+%Y-%m-%d %H:%M:%S')] 최신 이미지 pull: $TARGET_IMAGE"
docker pull "$TARGET_IMAGE"

for CONTAINER in mm-app mm-nginx; do
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 컨테이너 재시작: $CONTAINER"
    docker restart "$CONTAINER"
    sleep 3
done

# 재시작 후 헬스체크 (최대 60초 대기)
echo "[$(date '+%Y-%m-%d %H:%M:%S')] mm-app 헬스체크 시작..."
for i in $(seq 1 12); do
    sleep 5
    STATUS=$(docker inspect --format='{{.State.Status}}' "mm-app" 2>/dev/null || echo "unknown")
    if [ "$STATUS" = "running" ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] ✅ mm-app 정상 기동 확인"
        break
    fi
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 대기 중... ($i/12) 상태: $STATUS"
    if [ "$i" = "12" ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] ❌ 헬스체크 실패 — 수동 확인 필요"
        exit 1
    fi
done

echo "[$(date '+%Y-%m-%d %H:%M:%S')] 프로덕션 배포 완료"
