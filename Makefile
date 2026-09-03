.PHONY: tidy build test lint up down logs sd-card smoke soak clean

tidy:
	cd backend && go mod tidy

build:
	cd backend && go build -o ../bin/ingest ./cmd/ingest/

test:
	cd backend && go test ./... -v -count=1 -race

lint:
	cd backend && go vet ./...
	shellcheck -S warning -e SC1090 -s sh sdcard/initrun.sh sdcard/scripts/*.sh
	shellcheck -S warning -e SC2010 -s bash prep-sdcard.sh

up:
	docker compose up -d --build
	@echo "Backend: http://localhost:8090/health"
	@echo "MinIO:   http://localhost:9001"
	@echo "MQTT:    localhost:1883"
	@echo "Ollama:  http://localhost:11434"

down:
	docker compose down

logs:
	docker compose logs -f ingest postgres migrate minio mosquitto ollama

smoke:
	sh scripts/pilot-smoke.sh

soak:
	sh scripts/soak-test.sh

sd-card:
	sh scripts/package-doorbell-pilot.sh

clean:
	rm -rf bin/ dist/
	cd backend && go clean
