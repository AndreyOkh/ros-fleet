# Technical Context

## Core Technologies
- **Язык разработки:** Go 1.27.4 (`go.mod` указывает `go 1.27.4`).
- **JSON движок:** `encoding/json/v2` и `encoding/json/jsontext`.
- **SSH/SFTP:** `golang.org/x/crypto/ssh`, `github.com/pkg/sftp`.
- **Frontend:** HTMX (вшит через `embed.FS`), чистый CSS3 с CSS Variables для темной/светлой темы.
- **Интеграция с Git:** Системный бинарник `git` без CGO.
- **Версионирование и сборка:** `runtime/debug.ReadBuildInfo()` + `-ldflags` overrides в `internal/version`.
- **Движок обновлений:** Stdlib (`net/http`, `archive/tar`, `compress/gzip`, `crypto/sha256`) в `internal/updater`.
- **Контейнеризация:** Multi-stage Dockerfile (`ARG TARGETOS`, `ARG TARGETARCH`, `ENV DOCKER_CONTAINER=1`), Docker Compose (`compose.yaml` с `ghcr.io/andreyokh/ros-fleet:latest`), скрипт инициализации `docker-entrypoint.sh` с автоматическим исправлением прав томов через `su-exec` (поддержка `PUID`/`PGID`).
- **CI/CD:** GitHub Actions (`.github/workflows/ci.yml`, `.github/workflows/release.yml`) для статических бинарников `linux/amd64`, `linux/arm64`, архивов `tar.gz` и multi-arch образов GHCR (`ghcr.io/andreyokh/ros-fleet`).
- **Демонизация:** `ros-fleet.service` (Systemd unit).

## Runtime Requirements
- Системный `git` доступен в `$PATH`.
- Файлы конфигурации:
  - `config.json`: Основной конфигурационный файл (устройства, defaults, расписание, telegram, web, storage).
  - `state.json`: Генерируемый сервисом файл с историей телеметрии и бэкапов.
- Каталоги:
  - `backups/binary/` — каталог бинарных резервных копий.
  - `backups/exports/` — каталог текстовых экспортов (локальный Git-репозиторий).

## Build & Run Commands
- `make build` или `go build -o bin/ros-fleet ./cmd/ros-fleet`: сборка бинарника.
- `make build-static`: сборка статического бинарника без CGO.
- `make run` или `./bin/ros-fleet -config config.json`: запуск сервиса.
- `./bin/ros-fleet -version`: вывод версии, коммита и даты сборки.
- `./bin/ros-fleet -self-update`: автоматическое безопасное обновление Linux-бинарника.
- `./bin/ros-fleet -run-now`: однократный запуск цикла бэкапа и выход.
- `docker compose up -d`: запуск в контейнере через GHCR образ.
- `docker compose pull && docker compose up -d`: обновление контейнера.
