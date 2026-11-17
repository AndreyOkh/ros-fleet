# RealMikrotikBackup

Высокопроизводительное, надежное и идиоматичное консольное приложение на **Go 1.24+** для автоматического резервного копирования сетевого оборудования **MikroTik RouterOS (версий v6 и v7)**.

## Ключевые возможности и инженерные решения

- **Минимум внешних зависимостей:**
  - Вся работа с SSH построена на официальном `golang.org/x/crypto/ssh`.
  - Скачивание файлов по безопасному протоколу SFTP через `github.com/pkg/sftp`.
  - Никаких тяжелых фреймворков: `encoding/json` для конфигурации, `net/http` для Telegram API, `log/slog` для структурированного логирования.
- **Поддержка RouterOS v6 и RouterOS v7:**
  - Умная поддержка sensitive-данных: на RouterOS v7 выполняется `/export show-sensitive file=...`, с автоматическим бесшовным фолбэком на классический `/export file=...` на RouterOS v6.
  - Поддержка шифрования бинарного бэкапа: при заданном пароле `/system backup save name=... password="..."`, при отключенном шифровании используется обязательный для v7 флаг `dont-encrypt=yes` (с фолбэком для старых v6).
- **Конкурентность и безопасность:**
  - Ограничение числа параллельных соединений (Worker Pool / Semaphore) через `concurrency` в конфигурации.
  - Таймауты и отмена операций на базе `context.Context` на каждом этапе (dial, handshake, exec, sftp, write).
  - Поддержка Graceful Shutdown (`SIGINT`, `SIGTERM`): начатые бэкапы завершаются корректно, новые не стартуют.
- **Бережное отношение к Flash-памяти роутеров:**
  - Автоматическое ожидание и валидация готовности асинхронной записи файлов на накопитель MikroTik (root или `flash/`).
  - Проверка целостности локального файла (размер > 0 байт) перед удалением с устройства.
  - Гарантированная зачистка временных файлов `.rsc` и `.backup` с роутера через CLI RouterOS и SFTP.
- **Локальная ротация (Retention Policy):**
  - Автоматическое удаление архивов старше заданного `retention_days`.
  - Чистая файловая структура: `<backup_dir>/<router_name>/<YYYY-MM-DD>/<router_name>_<YYYY-MM-DD_HH-MM-SS>.[rsc|backup]`.
- **Telegram-уведомления:**
  - Подробный и красивый сводный отчет в формате HTML.
  - Автоматическое экранирование спецсимволов.
  - Безопасное разбиение сообщений, если отчет превышает лимит Telegram (4096 символов).
  - Возможность отключить отчеты при полном успехе (`notify_on_success: false`).

---

## Структура проекта

```
real-mikrotik-backup/
├── cmd/
│   └── backup/
│       └── main.go                 # Точка входа, флаги, slog, OS signals, scheduler
├── internal/
│   ├── config/
│   │   ├── config.go              # Загрузка, валидация и дефолты config.json
│   │   └── config_test.go         # Тесты валидации и дефолтов
│   ├── mikrotik/
│   │   └── client.go              # SSH/SFTP клиент, RouterOS v6/v7 команды, polling, cleanup
│   ├── notifier/
│   │   ├── telegram.go            # HTML-отчет и HTTP-клиент Telegram API
│   │   └── telegram_test.go       # Тесты форматирования, экранирования и chunking
│   ├── runner/
│   │   ├── runner.go              # Оркестрация бэкапа, Worker Pool, сбор метрик
│   │   └── runner_test.go         # Тесты конкурентности и context cancellation
│   └── storage/
│       ├── storage.go             # Атомарная запись (.tmp -> target), проверка и ротация
│       └── storage_test.go        # Тесты путей, санитизации и ротации
├── config.example.json            # Пример конфигурации
├── Dockerfile                     # Мультистейдж сборка (минимальный Alpine)
├── docker-compose.yml             # Запуск в Docker Compose
├── mikrotik-backup.service        # Systemd unit для Linux
├── Makefile                       # Таргеты сборки и тестов
├── go.mod
└── go.sum
```

---

## Конфигурация (`config.json`)

Скопируйте пример:
```bash
cp config.example.json config.json
```

```json
{
  "backup_dir": "./backups",
  "retention_days": 30,
  "concurrency": 5,
  "timeout_sec": 120,
  "schedule_interval": "24h",
  "insecure_ignore_host_key": true,
  "telegram": {
    "enabled": true,
    "bot_token": "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ",
    "chat_id": "-1001234567890",
    "notify_on_success": true
  },
  "routers": [
    {
      "name": "Router-Core-CCR2004",
      "host": "192.168.1.1",
      "port": 22,
      "user": "backup_user",
      "private_key_path": "/home/user/.ssh/id_ed25519",
      "private_key_passphrase": "",
      "insecure_ignore_host_key": true,
      "export_sensitive": true,
      "encrypt_backup": true,
      "backup_password": "SuperSecretPassword123!"
    },
    {
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

### Описание параметров

| Параметр | Тип | Описание |
|---|---|---|
| `backup_dir` | string | Путь к директории для хранения бэкапов (по умолчанию `./backups`) |
| `retention_days` | int | Срок хранения бэкапов в днях (0 = ротация отключена) |
| `concurrency` | int | Максимальное число параллельных сессий к роутерам (по умолчанию 5) |
| `timeout_sec` | int | Таймаут на одну операцию бэкапа роутера (по умолчанию 120 сек) |
| `schedule_interval`| string | Интервал запуска в режиме демона (`"24h"`, `"12h"`, `"30m"`) |
| `insecure_ignore_host_key` | bool | Глобальное отключение строгой проверки Host Key |
| `telegram.enabled` | bool | Включение отправки отчетов в Telegram |
| `telegram.bot_token` | string | Токен Telegram бота |
| `telegram.chat_id` | string | ID чата или группы (для групп обычно начинается с `-100`) |
| `telegram.notify_on_success`| bool | Отправлять ли отчет, если все бэкапы завершились успешно |
| `routers[].name` | string | Имя роутера (используется в директориях и именах файлов) |
| `routers[].host` | string | IP-адрес или DNS-имя |
| `routers[].port` | int | SSH-порт (по умолчанию 22) |
| `routers[].user` | string | Пользователь RouterOS |
| `routers[].password` | string | Пароль (опционально, если используется ключ) |
| `routers[].private_key_path` | string | Путь к SSH приватному ключу (OpenSSH / RSA / ED25519) |
| `routers[].private_key_passphrase` | string | Пароль приватного ключа (если ключ зашифрован) |
| `routers[].insecure_ignore_host_key` | bool | Локальный оверрайд проверки SSH host key для конкретного роутера |
| `routers[].export_sensitive` | bool | Включать ли пароли/ключи в `.rsc` экспорт (для RouterOS v7) |
| `routers[].encrypt_backup` | bool | Шифровать ли бинарный `.backup` файл |
| `routers[].backup_password` | string | Пароль шифрования бинарного бэкапа |

---

## Сборка

### 1. Локальная сборка
```bash
go build -o bin/real-mikrotik-backup ./cmd/backup
```

### 2. Кросс-компиляция статического бинарника без CGO (`CGO_ENABLED=0`)
Для Linux amd64:
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags="-s -w -extldflags '-static'" \
  -o bin/real-mikrotik-backup-linux-amd64 \
  ./cmd/backup
```

Для Linux arm64 (Raspberry Pi / ARM-серверы):
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
  -trimpath \
  -ldflags="-s -w -extldflags '-static'" \
  -o bin/real-mikrotik-backup-linux-arm64 \
  ./cmd/backup
```

Либо через Makefile:
```bash
make build-static
```

---

## Запуск

### Флаги командной строки

- `-config string`: путь к файлу конфигурации (по умолчанию `"config.json"`).
- `-once`: запустить бэкап один раз и завершить процесс (идеально для внешних cron-задач).
- `-log-level string`: уровень логирования (`debug`, `info`, `warn`, `error`; по умолчанию `"info"`).
- `-log-format string`: формат логов (`text` для консоли, `json` для систем сбора логов типа Vector/Loki).
- `-version`: показать версию приложения.

### Однократный запуск
```bash
./bin/real-mikrotik-backup -config config.json -once
```

### Фоновый режим (Daemon)
```bash
./bin/real-mikrotik-backup -config config.json
```
При запуске без флага `-once` приложение:
1. Выполняет начальный цикл бэкапа сразу при старте.
2. Запускает планировщик с интервалом `schedule_interval`.
3. Ожидает сигналы `SIGINT`/`SIGTERM` для плавного завершения.

---

## Развертывание

### Вариант 1: Systemd (Linux)

1. Скопируйте скомпилированный бинарник:
   ```bash
   sudo mkdir -p /opt/mikrotik-backup
   sudo cp bin/real-mikrotik-backup-linux-amd64 /opt/mikrotik-backup/mikrotik-backup
   sudo cp config.json /opt/mikrotik-backup/config.json
   ```
2. Создайте пользователя:
   ```bash
   sudo useradd -r -s /bin/false mikrotik-backup
   sudo chown -R mikrotik-backup:mikrotik-backup /opt/mikrotik-backup
   sudo chmod 600 /opt/mikrotik-backup/config.json
   ```
3. Установите systemd unit:
   ```bash
   sudo cp mikrotik-backup.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now mikrotik-backup.service
   ```
4. Просмотр логов:
   ```bash
   journalctl -u mikrotik-backup.service -f
   ```

### Вариант 2: Docker Compose

1. Настройте `config.json`.
2. Запустите контейнер:
   ```bash
   docker compose up -d
   ```
3. Просмотр логов:
   ```bash
   docker compose logs -f
   ```

---

## Настройка пользователя в RouterOS (Best Practices)

Для максимальной безопасности на роутерах MikroTik рекомендуется создать отдельную группу и пользователя с минимально необходимыми правами:

```routeros
# Создаем группу с правами чтения, бэкапа, экспорта, файлов и ssh/ftp
/user group add name=backup-group policy=read,write,policy,test,password,sensitive,ftp

# Создаем пользователя
/user add name=backup_user group=backup-group password="StrongPasswordHere"

# (Опционально) Привязка SSH-ключа
/user ssh-keys import public-key-file=id_ed25519.pub user=backup_user
```
