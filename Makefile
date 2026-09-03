.PHONY: build test up down logs sd-card clean lint

# ---- Build ----
build:
	cd backend && go build -o ../bin/ingest ./cmd/ingest/

# ---- Test ----
test:
	cd backend && go test ./... -v -count=1 -race

# ---- Lint ----
lint:
	cd backend && go vet ./...
	shellcheck sdcard/initrun.sh sdcard/scripts/*.sh || true

# ---- Docker ----
up:
	docker compose up -d
	@echo ""
	@echo "Services starting..."
	@echo "  Backend:    http://localhost:8090/health"
	@echo "  MinIO:      http://localhost:9001"
	@echo "  PostgreSQL: localhost:5432"
	@echo "  MQTT:       localhost:1883"
	@echo "  Ollama:     http://localhost:11434"

down:
	docker compose down

logs:
	docker compose logs -f ingest

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
	@cd dist && zip -r secure4k-sidecar-sdcard.zip sdcard/
	@echo ""
	@echo "SD card package ready: dist/secure4k-sidecar-sdcard.zip"
	@echo ""
	@echo "To flash:"
	@echo "  1. Format microSD as FAT32"
	@echo "  2. Unzip secure4k-sidecar-sdcard.zip to the root"
	@echo "  3. Edit sdcard/config/device.conf"
	@echo "  4. Insert into camera, power cycle"

# ---- Simulate ----
simulate-register:
	curl -s -X POST http://localhost:8090/v1/devices/register \
		-H "Authorization: Bearer $$(grep VALID_API_KEYS .env | cut -d= -f2 | cut -d, -f1)" \
		-H "Content-Type: application/json" \
		-d '{"api_key":"test","mac":"aa:bb:cc:dd:ee:ff","chip_info":"ingenic_t31","kernel":"3.10.14","mem_kb":65536,"sidecar_version":"0.1.0"}' | python3 -m json.tool

simulate-heartbeat:
	curl -s -X POST http://localhost:8090/v1/devices/heartbeat \
		-H "Authorization: Bearer $$(grep VALID_API_KEYS .env | cut -d= -f2 | cut -d, -f1)" \
		-H "Content-Type: application/json" \
		-d '{"device_id":"dev_test1234","ts":'$$(date +%s)',"mem_free_kb":32000,"disk_pct":45,"uptime_s":86400,"buffered_frames":0,"temp_mc":52000,"version":"0.1.0"}' | python3 -m json.tool

# ---- Clean ----
clean:
	rm -rf bin/ dist/
	cd backend && go clean
