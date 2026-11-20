# System Patterns & Architecture

## Architecture Overview
`ros-fleet` спроектирован по модульному принципу на стандартной библиотеке Go с чистыми границами между пакетами:

```
cmd/ros-fleet/main.go  --> Точка входа, парсинг CLI флагов, инициализация сервисов
internal/
├── config/            --> Чтение config.json, валидация, авто-миграция v1->v2, defaults
├── gitrepo/           --> Git CLI обертка, нормализация RouterOS header, commit, push
├── mikrotik/          --> SSH клиент, SFTP загрузка, выполнение команд ROS v6/v7
├── notifier/          --> Telegram Bot API клиент, форматирование HTML отчетов
├── runner/            --> Координация процесса бэкапа парка роутеров
├── scheduler/         --> Планировщик по расписанию (Time-of-Day с часовыми поясами)
├── state/             --> Персистентное хранилище state.json (RWMutex, атомарная запись)
├── storage/           --> Управление бинарными бэкапами и ротация по retention_days
├── updater/           --> GitHub releases client, SemVer, Docker detection, Linux self-update
├── version/           --> Гибридное версионирование (ReadBuildInfo + -ldflags)
└── web/               --> Встроенный HTMX Web UI (embed.FS, SSE / Polling, Basic Auth)
```

## Key Technical Patterns
1. **Zero External Heavy Frameworks:**
   - Веб-сервер на стандартном `net/http` и `html/template`.
   - JSON парсинг на экспериментальном/новом стандарте `encoding/json/v2` (Go 1.27).
   - Логирование исключительно через `log/slog`.
   - Фронтенд на HTMX и нативном CSS без Node/NPM.
2. **Безопасная персистентность состояния (`internal/state`):**
   - Доступ защищен через `sync.RWMutex`.
   - Запись состояния в `state.json` происходит атомарно (запись во временный файл `state.json.tmp` + `os.Rename`), предотвращая повреждение при сбоях.
3. **Нормализация экспортов (`internal/gitrepo`):**
   - Строка `# YYYY-MM-DD HH:MM:SS by RouterOS X.Y.Z` заменяется на `# RouterOS X.Y.Z`.
   - Репозиторий Git содержит только содержательные изменения.
4. **Разделение хранилищ:**
   - `binary_backup_dir`: Ротируемые бинарные файлы с таймстемпами.
   - `export_dir`: Рабочее дерево локального Git-репозитория с фиксированными именами файлов.
5. **Наследование настроек (`Defaults`):**
   - Роутеры могут наследовать глобальные параметры (порт, пользователь, пароль, SSH-ключ, пароль шифрования бэкапа, sensitive export) или переопределять их.
   - В UI это реализовано через 3-позиционные/сегментные переключатели (По умолчанию / Включено / Отключено).
6. **Гибридное версионирование и безопасное обновление (`internal/version`, `internal/updater`):**
   - Определение версии из `runtime/debug.ReadBuildInfo()` с бесшовным переопределением через `-ldflags` при компиляции в контейнере/CI.
   - Детектирование обновлений через GitHub Releases API (с заголовками `Accept: application/vnd.github+json` и `User-Agent: ros-fleet`).
   - SemVer компаратор с отсечением суффиксов `+dirty` и dev-версий.
   - Безопасный Linux self-update: проверка контрольных сумм по `sha256sums.txt`, распаковка архива `ros-fleet_${VERSION}_linux_${ARCH}.tar.gz` во временный файл в каталоге исполняемого файла (`filepath.Dir(execPath)`) для исключения `EXDEV`, копирование прав доступа, атомарный swap через `.old` с откатом.
   - Определение окружения Docker (`/.dockerenv`, `ENV DOCKER_CONTAINER=1`) и запрет self-update с рекомендацией обновить контейнер.
7. **CI/CD и контейнеризация (GitHub Actions & Multi-Arch Docker):**
   - Единый CI-пайплайн (`ci.yml`) с проверкой `go vet`, `go test -race`, статической компиляции и сборки Dockerfile с Buildx.
   - Релизный пайплайн (`release.yml`) по тегам `v*`: компиляция CGO_ENABLED=0 для `amd64` и `arm64`, жесткое именование архивов `ros-fleet_${VERSION}_linux_${ARCH}.tar.gz` (с сохранением префикса `v`), генерация `sha256sums.txt`, публикация GitHub Release и публикация multi-arch Docker-образа в GHCR со строго строчным именем репозитория (`ghcr.io/andreyokh/ros-fleet`).
8. **Автоматическое управление правами доступа в Docker (`su-exec` & `docker-entrypoint.sh`):**
   - Контейнер стартует входной скрипт `/usr/local/bin/docker-entrypoint.sh` под `root` (UID 0), проверяет смонтированные тома (`/app/backups`, `/app/backups/binary`, `/app/backups/exports`, `/app/state.json`, `/home/rosfleet`) и, если они были автоматически созданы демоном Docker с владельцем `root:root`, выполняет `chown -R` на целевой UID/GID.
   - Поддерживает переменные `PUID`/`PGID` (по умолчанию `1000:1000`) для бесшовной интеграции с правами пользователя на хосте.
   - Сбрасывает привилегии `root` через легковесный Alpine инструмент `su-exec` перед выполнением бинарника `ros-fleet`.
   - Если контейнер запущен сразу под непривилегированным пользователем (например, Kubernetes `runAsNonRoot: true`), скрипт пропускает шаг `chown` и запускает процесс напрямую через `exec "$@"`.
9. **Автоматическое разрешение расхождений Git (`Auto-Fetch & Merge`):**
   - При отклонении `git push` (например, non-fast-forward из-за внешних коммитов в удаленном репозитории) сервис автоматически выполняет `git fetch origin <branch>` и слияние `git merge --no-edit --allow-unrelated-histories -X ours origin/<branch>`.
   - Стратегия `-X ours` гарантирует приоритет только что выгруженного с роутера конфига в случае конфликта и исключает блокировку процесса.
   - После успешного слияния выполняется повторный `git push`. При неудаче слияния вызывается `git merge --abort` для сохранения чистоты рабочей директории.
10. **Встроенный SSL/TLS и мультиплексирующий авторедирект (`internal/web/tls.go`):**
   - Полный отказ от сторонних криптографических библиотек — использование только стандартной библиотеки Go (`crypto/tls`, `crypto/x509`, `crypto/ecdsa`, `encoding/pem`, `net`, `bufio`).
   - Автоматическая генерация 10-летнего ECDSA P-256 сертификата с SAN при первом запуске и атомарное сохранение путей в `config.json` (`web.ssl_cert_file`, `web.ssl_key_file`).
   - Мультиплексирующий `net.Listener` (`splitListener`) с `bufio.Reader.Peek(1)`: байт `0x16` (TLS) передается в TLS-стек, а открытый HTTP-трафик перенаправляется `HTTP/1.1 308 Permanent Redirect` на `https://<Host><RequestURI>` на том же самом порту без падений с `400 Bad Request`.
   - Принудительный заголовок HSTS (`Strict-Transport-Security: max-age=31536000; includeSubDomains`).
   - Управление шифрованием исключительно со стороны сервера (`config.json` / CLI-флаг `-ssl`).
