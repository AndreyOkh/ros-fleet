# Technical Context

## Core Technologies
- **Язык разработки:** Go 1.27.4 (`go.mod` указывает `go 1.27.4`).
- **JSON движок:** `encoding/json/v2` и `encoding/json/jsontext`.
- **SSH/SFTP:** `golang.org/x/crypto/ssh`, `github.com/pkg/sftp`.
- **Frontend:** HTMX (вшит через `embed.FS`), чистый CSS3 с CSS Variables для темной/светлой темы.
- **Интеграция с Git:** Системный бинарник `git` без CGO.
- **Контейнеризация:** Docker (Multi-stage build на Alpine с OpenSSH и Git) + Docker Compose (`compose.yaml`).
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
- `make run` или `./bin/ros-fleet -config config.json`: запуск сервиса.
- `./bin/ros-fleet -run-now`: однократный запуск цикла бэкапа и выход.
- `docker compose up -d`: запуск в контейнере.
