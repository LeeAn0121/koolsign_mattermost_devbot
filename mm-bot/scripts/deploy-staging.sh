#!/bin/sh
set -e

TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')
echo "[$TIMESTAMP] 스테이징 배포 시작"

# 대상 컨테이너 (스테이징)
TARGET_CONTAINER="mm-app"
TARGET_IMAGE="mattermost/mattermost-team-edition:latest"

echo "[$(date '+%Y-%m-%d %H:%M:%S')] 최신 이미지 pull: $TARGET_IMAGE"
docker pull "$TARGET_IMAGE"

echo "[$(date '+%Y-%m-%d %H:%M:%S')] 컨테이너 재시작: $TARGET_CONTAINER"
docker restart "$TARGET_CONTAINER"

# 재시작 후 헬스체크 (최대 30초 대기)
for i in $(seq 1 6); do
    sleep 5
    STATUS=$(docker inspect --format='{{.State.Status}}' "$TARGET_CONTAINER" 2>/dev/null || echo "unknown")
    if [ "$STATUS" = "running" ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] ✅ $TARGET_CONTAINER 정상 기동 확인"
        break
    fi
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] 대기 중... ($i/6) 상태: $STATUS"
done

echo "[$(date '+%Y-%m-%d %H:%M:%S')] 스테이징 배포 완료"
