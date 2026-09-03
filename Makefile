.PHONY: tidy build test lint up down logs sd-card smoke soak clean

tidy:
	cd backend && go mod tidy

build:
	cd backend && go build -o ../bin/ingest ./cmd/ingest/

test:
	cd backend && go test ./... -v -count=1 -race

lint:
	cd backend && go vet ./...
	shellcheck -s sh sdcard/initrun.sh sdcard/scripts/*.sh
	shellcheck prep-sdcard.sh

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
	./scripts/pilot-smoke.sh

soak:
	./scripts/soak-test.sh

sd-card:
	@echo "Packaging SD card files..."
	@mkdir -p dist
	@rm -rf dist/sdcard
	@cp -r sdcard dist/sdcard
	@chmod +x dist/sdcard/initrun.sh dist/sdcard/scripts/*.sh
	@cd dist/sdcard && ln -sf initrun.sh run.sh
	@cd dist/sdcard && ln -sf initrun.sh custom.sh
	@mkdir -p dist/sdcard/buffer
	@cd dist && zip -r secure4k-sidecar-sdcard.zip sdcard/
	@echo "SD card package ready: dist/secure4k-sidecar-sdcard.zip"

clean:
	rm -rf bin/ dist/
	cd backend && go clean
