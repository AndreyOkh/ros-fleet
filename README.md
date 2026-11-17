# ros-fleet

Высокопроизводительная, надежная система управления парком сетевого оборудования **MikroTik RouterOS (v6 и v7)** и автоматического резервного копирования на **Go 1.24+**.

Модуль: `github.com/AndreyOkh/ros-fleet`

## Возможности

- **Встроенный Web-интерфейс (Zero-Build / Single Binary):**
  - Работает без внешних Node.js/npm зависимостей: весь фронтенд вшит в бинарник через `embed.FS`.
  - Реактивный UI на базе **HTMX** (динамическое переключение тогглов, запуск бэкапа, опрос роутеров без перезагрузки страницы).
  - Минималистичный и чистый CSS с поддержкой **темной и светлой темы**.
  - Опциональная авторизация через **HTTP Basic Auth** (`web.auth_user`, `web.auth_password`).
- **Сбор телеметрии в рантайме (In-Memory State):**
  - Опрос оборудования по SSH (`/system resource print` и `/system routerboard print`).
  - Определение модели (например, `RB4011iGS+`, `CCR2004-1G-12S+2XS`, `CHR`, `x86`) и версии RouterOS (например, `7.16.1 (stable)`).
  - Отслеживание онлайн-статуса и истории последнего бэкапа (успех / ошибка с деталями).
  - Обновление информации как по расписанию/бэкапу, так и по кнопке «Обновить инфо» в дашборде.
- **Потокобезопасная работа с конфигурацией:**
  - `ConfigManager` с `sync.RWMutex` для безопасного чтения и изменения конфигурации из веб-интерфейса на лету.
  - Атомарное сохранение изменений в `config.json` (запись во временный файл + `os.Rename`).
  - Флаг `enabled` для каждого роутера: можно временно отключать бэкап устройства без его удаления.
- **Поддержка RouterOS v6 и RouterOS v7:**
  - Умная поддержка sensitive-данных: `/export show-sensitive file=...` на v7 с автоматическим фолбэком на классический `/export file=...` на v6.
  - Поддержка шифрования бинарного бэкапа: при заданном пароле `/system backup save name=... password="..."`, при отключенном шифровании используется флаг `dont-encrypt=yes` (RouterOS v7) с фолбэком для старых v6.
- **Безопасность и конкурентность:**
  - Ограничение одновременных подключений (Worker Pool / Semaphore) через параметр `concurrency`.
  - Таймауты и отмена операций на базе `context.Context`.
  - Плавное завершение работы (Graceful Shutdown) по сигналам `SIGINT` и `SIGTERM`.
- **Бережное отношение к Flash-памяти роутеров:**
  - Ожидание завершения асинхронной записи файлов на накопитель роутера (root или `flash/`).
  - Проверка целостности локального файла (размер > 0 байт) перед удалением с устройства.
  - Очистка временных файлов `.rsc` и `.backup` с роутера через CLI и SFTP.
- **Локальная ротация (Retention Policy):**
  - Удаление архивов старше заданного `retention_days`.
  - Структура: `<backup_dir>/<router_name>/<YYYY-MM-DD>/<router_name>_<YYYY-MM-DD_HH-MM-SS>.[rsc|backup]`.
- **Telegram-уведомления:**
  - HTML-отчет с экранированием спецсимволов и авторазбивкой длинных сообщений.

---

## Структура проекта

```
ros-fleet/
├── cmd/
│   └── ros-fleet/
│       └── main.go                 # Точка входа, флаги CLI, slog, graceful shutdown, планировщик
├── internal/
│   ├── config/
│   │   ├── config.go              # Модель конфигурации, Manager (sync.RWMutex), атомарный save
│   │   └── config_test.go         # Unit-тесты парсинга, валидации и Manager
│   ├── mikrotik/
│   │   └── client.go              # SSH/SFTP клиент, сбор DeviceInfo, RouterOS v6/v7 команды, cleanup
│   ├── notifier/
│   │   ├── telegram.go            # HTML-отчет и HTTP-клиент Telegram API
│   │   └── telegram_test.go       # Unit-тесты Telegram-уведомлений
│   ├── runner/
│   │   ├── runner.go              # Worker Pool, оркестрация, RunSingle, RefreshDevice
│   │   └── runner_test.go         # Unit-тесты конкурентности и context cancellation
│   ├── state/
│   │   └── state.go               # In-memory реестр устройств (sync.RWMutex), статусы, телеметрия
│   ├── storage/
│   │   ├── storage.go             # Атомарная запись (.tmp -> target), валидация, ротация
│   │   └── storage_test.go        # Unit-тесты storage и ротации
│   └── web/
│       ├── server.go              # HTTP-сервер (Go 1.22+ routing), Basic Auth, HTMX-обработчики
│       └── server_test.go         # Интеграционные тесты веб-интерфейса и API
├── web/
│   ├── web.go                     # embed.FS для шаблонов и статики
│   ├── static/
│   │   ├── htmx.min.js            # Автономный embedded HTMX
│   │   └── style.css              # Адаптивные стили (Dark/Light mode)
│   └── templates/
│       ├── index.html             # Главная страница дашборда
│       ├── summary_cards.html     # KPI-карточки сводки
│       ├── table_container.html   # Контейнер таблицы устройств
│       ├── table_row.html         # Шаблон строки устройства (HTMX swap)
│       └── router_modal.html      # Модальное окно добавления/редактирования
├── compose.yaml                   # Docker Compose спецификация
├── Dockerfile                     # Мультистейдж сборка статического бинарника
├── ros-fleet.service              # Systemd unit для Linux
├── config.example.json            # Пример конфигурации
├── Makefile                       # Сборка, тесты и кросс-компиляция
├── go.mod
└── go.sum
```

---

## Конфигурация (`config.json`)

```json
{
  "backup_dir": "./backups",
  "retention_days": 30,
  "concurrency": 5,
  "timeout_sec": 120,
  "schedule_interval": "24h",
  "insecure_ignore_host_key": true,
  "web": {
    "enabled": true,
    "listen_addr": ":8080",
    "auth_user": "admin",
    "auth_password": ""
  },
  "telegram": {
    "enabled": true,
    "bot_token": "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ",
    "chat_id": "-1001234567890",
    "notify_on_success": true
  },
  "routers": [
    {
      "enabled": true,
      "name": "Router-Core-CCR2004",
      "host": "192.168.1.1",
      "port": 22,
      "user": "backup_user",
      "private_key_path": "/home/user/.ssh/id_ed25519",
      "private_key_passphrase": "",
      "insecure_ignore_host_key": true,
      "export_sensitive": true,
      "encrypt_backup": true,
      "backup_password": "SuperSecretBackupPassword123!"
    },
    {
      "enabled": true,
      "name": "Router-Office-RB4011",
      "host": "192.168.10.1",
      "port": 22,
      "user": "backup_user",
      "password": "RouterPasswordExample",
      "insecure_ignore_host_key": true,
      "export_sensitive": true,
      "encrypt_backup": false
    }
  ]
}
```

---

## Сборка и запуск

### 1. Статический бинарник без CGO:
```bash
make build-static
```
Или вручную:
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags="-s -w -extldflags '-static'" \
  -o bin/ros-fleet-linux-amd64 \
  ./cmd/ros-fleet
```

### 2. Запуск в фоновом режиме (демон + Web UI):
```bash
./bin/ros-fleet-linux-amd64 -config config.json
```
После запуска откройте в браузере: `http://localhost:8080`.

### 3. Однократный запуск бэкапа без веб-сервера (для cron):
```bash
./bin/ros-fleet-linux-amd64 -config config.json -once
```

### 4. Запуск в Docker Compose:
```bash
docker compose up -d
```
Логи:
```bash
docker compose logs -f
```

### 5. Запуск через systemd:
```bash
sudo cp bin/ros-fleet-linux-amd64 /opt/ros-fleet/ros-fleet
sudo cp config.example.json /opt/ros-fleet/config.json
sudo cp ros-fleet.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ros-fleet.service
```
