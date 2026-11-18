# Project Guidelines & Rules

## Memory Bank & Context Management
Для сохранения непрерывности контекста между сессиями и задачами в данном проекте используется система **Memory Bank**.
Все файлы контекста расположены в каталоге:
`./.agents/memory-bank/`

### Правила работы с контекстом:
1. **Чтение контекста перед началом работы:**
   - Обязательно прочитай [activeContext.md](./.agents/memory-bank/activeContext.md) для получения актуального состояния и текущего фокуса.
   - Ознакомься с [projectbrief.md](./.agents/memory-bank/projectbrief.md) и [systemPatterns.md](./.agents/memory-bank/systemPatterns.md) перед внесением изменений в архитектуру или кодовую базу.
   - Подробные правила описаны в [.agents/rules/memory-bank.md](./.agents/rules/memory-bank.md).
2. **Запись и обновление контекста по завершении работы:**
   - Перед завершением ответа или серии правок обнови [activeContext.md](./.agents/memory-bank/activeContext.md) и [progress.md](./.agents/memory-bank/progress.md).
   - Если добавлены архитектурные решения или изменен стек, обнови [systemPatterns.md](./.agents/memory-bank/systemPatterns.md) и [techContext.md](./.agents/memory-bank/techContext.md).
3. **Архитектурные ограничения проекта:**
   - **Go 1.27.4** и стандартная библиотека (включая `encoding/json/v2`).
   - Отказ от сторонних тяжелых фреймворков и Node.js (UI на встроенном HTMX через `embed.FS`).
   - Нормализация заголовков экспортов RouterOS в `internal/gitrepo` для чистого `git diff`.
   - Потокобезопасная атомарная запись `state.json` через временный файл.
