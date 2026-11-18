# ros-fleet

[![Go Version](https://img.shields.io/badge/Go-1.27.4-00ADD8?style=flat&logo=go)](https://go.dev/)
[![MikroTik RouterOS](https://img.shields.io/badge/RouterOS-v6%20%7C%20v7-black?style=flat&logo=mikrotik)](https://mikrotik.com)
[![Front-end](https://img.shields.io/badge/UI-HTMX%20%2B%20Vanilla%20CSS-blue?style=flat)](https://htmx.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Высокопроизводительная, надежная система централизованного управления парком сетевого оборудования **MikroTik RouterOS (v6 и v7)**, автоматического резервного копирования и версионирования конфигураций в Git на **Go 1.27** с использованием стандартного пакета **`encoding/json/v2`**.

Модуль: `github.com/AndreyOkh/ros-fleet`

---

## Ключевые возможности

- **Минимум внешних зависимостей:**
  - Полный отказ от тяжелых фреймворков. Приложение построено на стандартной библиотеке Go 1.27.4:
    - Официальный `encoding/json/v2` и `encoding/json/jsontext`.
    - Стандартный `log/slog` для структурированного логирования.
    - Официальный `golang.org/x/crypto/ssh` и проверенный `github.com/pkg/sftp`.
    - Git-интеграция через системный `git` CLI без CGO и сторонних Go-библиотек.
- **Встроенный Web-интерфейс (Zero-Build / Single Binary):**
  - Никакого Node.js, npm или webpack: все шаблоны, стили и HTMX вшиты в единственный бинарник через `embed.FS`.
  - Динамический UI на базе **HTMX**: добавление/редактирование роутеров, ручной запуск бэкапа, опрос телеметрии без перезагрузки страниц.
  - Адаптивный интерфейс с автоматической поддержкой темной и светлой темы.
  - Опциональная защита дашборда через **HTTP Basic Auth** (`web.auth_user`, `web.auth_password`).
  - Отображение статуса Git-репозитория прямо на дашборде (ветка, хэш, время последнего коммита, статус push).
- **Сбор телеметрии и персистентное состояние (`state.json`):**
  - Опрос оборудования по SSH (`/system resource print` и `/system routerboard print`).
  - Распознавание моделей (например, `RB4011iGS+`, `CCR2004-1G-12S+2XS`, `CHR`, `x86`) и версий RouterOS (например, `7.16.1 (stable)`).
  - Персистентное хранилище `state.json` (`sync.RWMutex`) с атомарной перезаписью: при старте сервиса Web UI сразу отображает актуальное состояние без повторного опроса устройств.
- **Глобальные настройки подключения (`defaults`):**
  - Верхнеуровневый блок `defaults` в `config.json` для общих портов, логинов, SSH-ключей и паролей шифрования.
  - Устройства могут наследовать глобальные настройки или переопределять любые параметры индивидуально.
  - Лаконичное модальное окно добавления/редактирования устройства: удобный переключатель «Использовать настройки по умолчанию» и чистые чекбоксы.
- **Разделение бинарных бэкапов и Git-каталога экспортов:**
  - `binary_backup_dir` — каталог для бинарных файлов `.backup` с таймстемпами. К этой директории применяется автоматическая ротация по `retention_days`.
  - `export_dir` — изолированный каталог для актуальных конфигураций `.rsc` (`<router_name>.rsc`), являющийся локальным Git-репозиторием.
- **Умное версионирование конфигураций в Git (`internal/gitrepo`):**
  - **Нормализация RouterOS Header:** автоматическое отсечение динамической строки даты/времени (`# 2026-11-17 03:00:05 by RouterOS 7.16.1` -> `# RouterOS 7.16.1`), благодаря чему `git diff` видит только реальные изменения конфигурации, не создавая мусорных коммитов при каждом прогоне.
  - Единый цикл коммита: после опроса всех роутеров сервис делает один понятный коммит (например, `backup: update configs (Router1, Router2)`).
  - Опциональный автоматический `git push` в удаленный Git-репозиторий (GitHub, GitLab, Gitea).
- **Планировщик по расписанию (Time-of-Day):**
  - Запуск в точное время суток (`schedule.times: ["03:00", "15:00"]`) с поддержкой часовых поясов (`schedule.timezone: "Europe/Moscow"`).
  - Отказ от немедленного бэкапа при старте: запуск по расписанию, по требованию из Web UI или через CLI-флаг `-run-now`.
- **Поддержка RouterOS v6 и RouterOS v7:**
  - Умная поддержка sensitive-данных: `/export show-sensitive file=...` на v7 с автоматическим фолбэком на классический `/export file=...` на v6.
  - Шифрование бэкапа: при заданном пароле `/system backup save name=... password="..."`, при отключенном шифровании используется флаг `dont-encrypt=yes` (RouterOS v7) с фолбэком для старых v6.
- **Telegram-уведомления:**
  - HTML-отчет с подсчетом успешных/ошибочных бэкапов, объемом данных и детализацией по каждому роутеру.

---

## Архитектура хранения файлов

```
backups/
├── binary/                                   # binary_backup_dir (ротация по retention_days)
│   ├── Router-Core/
│   │   ├── Router-Core_2026-11-16_03-00-01.backup
│   │   └── Router-Core_2026-11-17_03-00-02.backup
│   └── Router-Office/
│       ├── Router-Office_2026-11-16_03-00-05.backup
│       └── Router-Office_2026-11-17_03-00-04.backup
│
└── exports/                                  # export_dir (локальный Git-репозиторий)
    ├── .git/
    ├── Router-Core.rsc                       # Всегда последняя актуальная версия
    └── Router-Office.rsc                     # Нормализованный заголовок (без ложных diff)
```

1. **Бинарные бэкапы (`binary_backup_dir`):**
   - Сохраняются с полным таймстемпом: `<router_name>_<YYYY-MM-DD_HH-MM-SS>.backup`.
   - Фоновая ротация проверяет дату модификации файлов: копии старше `retention_days` автоматически удаляются, освобождая дисковое пространство.
2. **Текстовые экспорты (`export_dir`):**
   - Каждый роутер представлен фиксированным файлом `<router_name>.rsc`.
   - При каждом цикле бэкапа файл перезаписывается нормализованным экспортом.
   - Вся история изменений сохраняется в истории коммитов Git.

---

## Настройка Git-интеграции (Access Token или SSH-ключи)

Для автоматической отправки изменений конфигураций в удаленный репозиторий (`auto_push: true`) поддерживается два варианта авторизации:

### Вариант 1: Access Token через HTTPS (Рекомендуется)
Самый простой и безопасный способ без необходимости генерации и монтирования SSH-ключей. Поддерживает **Personal Access Token**, **Deploy Token**, **Project Access Token** (GitHub, GitLab, Gitea, Forgejo, Bitbucket).

1. В `config.json` укажите HTTPS-адрес репозитория и ваш токен:
```json
  "git": {
    "enabled": true,
    "remote_url": "https://github.com/your-org/mikrotik-configs.git",
    "branch": "main",
    "auto_push": true,
    "access_token": "ghp_yourPersonalAccessTokenHere"
  }
```
2. Или передайте токен через переменную окружения `GIT_ACCESS_TOKEN` (или `ROS_FLEET_GIT_TOKEN`) в Docker Compose:
```yaml
environment:
  - GIT_ACCESS_TOKEN=${GIT_ACCESS_TOKEN}
```
> [!NOTE]
> `ros-fleet` автоматически формирует корректные учетные данные для выбранного Git-сервера (GitHub `x-access-token`, GitLab `oauth2`, Bitbucket `x-token-auth`, Gitea) и гарантированно маскирует токен в логах и веб-интерфейсе (`[REDACTED]`).

---

### Вариант 2: Доступ по SSH-ключам

### 1. Генерация отдельного SSH-ключа (на хосте)
```bash
ssh-keygen -t ed25519 -C "ros-fleet@backup" -f ~/.ssh/id_ed25519 -N ""
```
Добавьте публичный ключ `~/.ssh/id_ed25519.pub` в качестве **Deploy Key** (с правами на запись / write access) в ваш репозиторий на GitHub / GitLab / Gitea.

### 2. Добавление хоста в known_hosts
```bash
ssh-keyscan github.com >> ~/.ssh/known_hosts
```

### 3. Проброс ключей в Docker Compose (`compose.yaml`)
В `compose.yaml` ключи и `known_hosts` монтируются в домашнюю директорию пользователя `rosfleet` в режиме `ro` (read-only):

```yaml
services:
  ros-fleet:
    build:
      context: .
      dockerfile: Dockerfile
    image: ros-fleet:latest
    container_name: ros-fleet
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./config.json:/app/config.json:rw
      - ./state.json:/app/state.json:rw
      - ./backups/binary:/app/backups/binary:rw
      - ./backups/exports:/app/backups/exports:rw
      # SSH-ключи для доступа к роутерам и Git push
      - ~/.ssh/id_ed25519:/home/rosfleet/.ssh/id_ed25519:ro
      - ~/.ssh/known_hosts:/home/rosfleet/.ssh/known_hosts:ro
    environment:
      - TZ=Europe/Moscow
    command: ["-config", "/app/config.json"]
```

---

## Конфигурация (`config.json`)

```json
{
  "binary_backup_dir": "./backups/binary",
  "export_dir": "./backups/exports",
  "state_file": "./state.json",
  "retention_days": 30,
  "concurrency": 5,
  "timeout_sec": 120,
  "schedule": {
    "times": [
      "03:00",
      "15:00"
    ],
    "timezone": "Europe/Moscow"
  },
  "defaults": {
    "port": 22,
    "user": "backup_user",
    "password": "",
    "private_key_path": "/home/rosfleet/.ssh/id_ed25519",
    "private_key_passphrase": "",
    "insecure_ignore_host_key": true,
    "export_sensitive": true,
    "encrypt_backup": false,
    "backup_password": ""
  },
  "git": {
    "enabled": true,
    "remote_url": "git@github.com:your-org/mikrotik-configs.git",
    "branch": "main",
    "author_name": "ros-fleet",
    "author_email": "ros-fleet@local",
    "auto_push": false
  },
  "web": {
    "enabled": true,
    "listen_addr": ":8080",
    "auth_user": "admin",
    "auth_password": ""
  },
  "telegram": {
    "enabled": false,
    "bot_token": "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ",
    "chat_id": "-1001234567890",
    "notify_on_success": true,
    "timeout_sec": 15
  },
  "routers": [
    {
      "enabled": true,
      "name": "Router-Core-CCR2004",
      "host": "192.168.1.1"
    },
    {
      "enabled": true,
      "name": "Router-Office-RB4011",
      "host": "192.168.10.1",
      "password": "RouterCustomPassword",
      "encrypt_backup": true,
      "backup_password": "SuperSecretBackupPassword123!"
    },
    {
      "enabled": false,
      "name": "Router-Warehouse-Legacy",
      "host": "10.50.0.1",
      "port": 2222,
      "user": "admin",
      "password": "LegacyPasswordExample",
      "export_sensitive": false
    }
  ]
}
```

### Описание параметров

| Параметр | Тип | По умолчанию | Описание |
| :--- | :---: | :---: | :--- |
| `binary_backup_dir` | string | `./backups/binary` | Каталог для бинарных бэкапов (`.backup`) |
| `export_dir` | string | `./backups/exports` | Каталог для текстовых экспортов (`.rsc`), является Git-репозиторием |
| `state_file` | string | `./state.json` | Файл персистентного состояния и телеметрии устройств |
| `retention_days` | int | `30` | Срок хранения бинарных бэкапов в днях (0 = отключено) |
| `concurrency` | int | `5` | Пул параллельных SSH-подключений |
| `timeout_sec` | int | `120` | Таймаут одной операции с роутером в секундах |
| `schedule.times` | []string | `["03:00"]` | Время суток для запуска бэкапов в формате `HH:MM` |
| `schedule.timezone` | string | `Local` | Часовой пояс планировщика (напр. `"Europe/Moscow"`, `"UTC"`) |
| `defaults.*` | object | — | Глобальные настройки авторизации и бэкапа по умолчанию |
| `git.enabled` | bool | `false` | Включить Git-версионирование экспортов в `export_dir` |
| `git.remote_url` | string | `""` | URL удаленного репозитория (`https://...` или `git@...`) |
| `git.branch` | string | `"main"` | Имя ветки в Git |
| `git.author_name` | string | `"ros-fleet"` | Имя автора для Git коммитов |
| `git.author_email` | string | `"ros-fleet@local"` | Email автора для Git коммитов |
| `git.auto_push` | bool | `false` | Выполнять `git push` при наличии новых коммитов |
| `git.access_token` | string | `""` | Personal / Deploy Access Token (также через `GIT_ACCESS_TOKEN` env) |
| `web.enabled` | bool | `true` | Включить встроенный HTTP веб-интерфейс |
| `web.listen_addr` | string | `":8080"` | Сетевой адрес и порт веб-сервера |
| `web.auth_user` | string | `""` | Логин для Basic Auth (если пусто — вход без пароля) |
| `web.auth_password` | string | `""` | Пароль для Basic Auth |
| `telegram.enabled` | bool | `false` | Включить отправку отчетов в Telegram |
| `routers[].enabled` | bool | `true` | Флаг активности устройства в парке |
| `routers[].name` | string | — | Уникальное имя устройства |
| `routers[].host` | string | — | IP-адрес или DNS-имя роутера |

---

## Флаги командной строки (CLI)

```bash
ros-fleet [флаги]
```

- `-config <путь>` — путь к конфигурационному файлу JSON (по умолчанию `config.json`).
- `-run-now` — выполнить немедленный цикл бэкапа всех устройств при старте приложения перед переходом в режим ожидания расписания.
- `-once` — выполнить один цикл бэкапа и сразу завершить работу (удобно для вызова из системного cron или пайплайнов).
- `-log-level <level>` — уровень логирования: `debug`, `info`, `warn`, `error` (по умолчанию `info`).
- `-log-format <format>` — формат логов: `text` или `json` (по умолчанию `text`).
- `-version` — вывести информацию о версии и дату сборки приложения.

---

## Сборка и запуск

### 1. Сборка из исходников на Go (Go 1.24+ / 1.27.4)

Статический бинарник без CGO:
```bash
make build-static
```
Или прямой вызов Go:
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags="-s -w -extldflags '-static'" \
  -o bin/ros-fleet-linux-amd64 \
  ./cmd/ros-fleet
```

### 2. Запуск в фоновом режиме (демон + Web UI)
```bash
./bin/ros-fleet-linux-amd64 -config config.json
```
Откройте в браузере: `http://localhost:8080`.

### 3. Запуск через Docker Compose
```bash
# Клонируйте репозиторий и подготовьте config.json
cp config.example.json config.json

# Запустите контейнер в фоне
docker compose up -d

# Просмотр логов
docker compose logs -f
```

### 4. Запуск через Systemd (Linux)
```bash
sudo cp bin/ros-fleet-linux-amd64 /usr/local/bin/ros-fleet
sudo mkdir -p /etc/ros-fleet
sudo cp config.example.json /etc/ros-fleet/config.json
sudo cp ros-fleet.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ros-fleet.service
```

---

## Лицензия

Проект распространяется под лицензией MIT.
