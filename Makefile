.PHONY: web build test dev-api dev-web clean
web:
	cd web && npm ci && npm run build
build: web
	go build -o llmmeter .
test:
	go test ./...
	cd web && npm test
dev-api:
	go run . serve -db dev.db -listen 127.0.0.1:8100
dev-web:
	cd web && npm run dev
clean:
	rm -f llmmeter dev.db dev.db-shm dev.db-wal
