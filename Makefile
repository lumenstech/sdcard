.PHONY: build test test-short race vet up down logs sd-card clean lint smoke help

help:
	@echo "Secure4K Sidecar Platform — available targets"
	@echo ""
	@echo "  make build      Build the ingest binary into ./bin/"
	@echo "  make test       Run all backend tests with -race"
	@echo "  make test-short Run only fast tests (no docker required)"
	@echo "  make vet        Run go vet"
	@echo "  make lint       go vet + (optional) shellcheck"
	@echo "  make up         docker compose up (reads .env)"
	@echo "  make down       docker compose down"
	@echo "  make logs       Tail ingest logs"
	@echo "  make smoke      Run pilot smoke test against http://localhost:8090"
	@echo "  make sd-card    Package sdcard/ into a flashable zip"
	@echo "  make clean      Remove build artefacts"

# ---- Build ----
build:
	cd backend && go build -o ../bin/ingest ./cmd/ingest/

# ---- Test ----
test:
	cd backend && go test ./... -count=1 -race

test-short:
	cd backend && go test ./... -count=1 -short

vet:
	cd backend && go vet ./...

lint: vet
	@command -v shellcheck >/dev/null 2>&1 && \
		shellcheck sdcard/initrun.sh sdcard/scripts/*.sh || \
		echo "shellcheck not installed; skipping"

# ---- Docker ----
up:
	@test -f .env || (echo "ERROR: missing .env (copy from .env.example)"; exit 1)
	docker compose --env-file .env up -d
	@echo ""
	@echo "Services starting..."
	@echo "  Backend:    http://localhost:8090/health"
	@echo "  MinIO:      http://localhost:9001 (minioadmin/minioadmin)"
	@echo "  PostgreSQL: localhost:5432 (secure4k/secure4k)"
	@echo "  MQTT:       localhost:1883"
	@echo "  Ollama:     http://localhost:11434"

down:
	docker compose down

logs:
	docker compose logs -f ingest

# ---- Pilot smoke test ----
# Exercises the full happy path: register -> frame -> list events.
smoke:
	@bash scripts/smoke.sh

# ---- SD Card Packaging ----
sd-card:
	@echo "Packaging SD card files..."
	@mkdir -p dist
	@rm -rf dist/sdcard
	@cp -r sdcard dist/sdcard
	@chmod +x dist/sdcard/initrun.sh dist/sdcard/scripts/*.sh
	# Create symlinks for different camera boot hooks
	@cd dist/sdcard && ln -sf initrun.sh run.sh
	@cd dist/sdcard && ln -sf initrun.sh custom.sh
	# Create buffer directory
	@mkdir -p dist/sdcard/buffer
	# Package
	@cd dist && zip -r secure4k-sidecar-sdcard.zip sdcard/ >/dev/null
	@echo ""
	@echo "SD card package ready: dist/secure4k-sidecar-sdcard.zip"
	@echo ""
	@echo "To flash:"
	@echo "  1. Format microSD as FAT32"
	@echo "  2. Unzip secure4k-sidecar-sdcard.zip to the root"
	@echo "  3. Edit sdcard/config/device.conf"
	@echo "  4. Insert into camera, power cycle"

# ---- Clean ----
clean:
	rm -rf bin/ dist/
	cd backend && go clean
