---
name: context-sync
description: Процедура чтения, синхронизации и обновления контекста Memory Bank в ./.agents/memory-bank
---

# Workflow: Синхронизация контекста Memory Bank

Этот рабочий процесс (workflow) регламентирует шаги по чтению контекста при старте задачи и его актуализации по итогам работы.

## Шаг 1: Чтение контекста (Context Ingestion)
1. Прочитать `activeContext.md` в `.agents/memory-bank/` для понимания текущего статуса и недавних задач.
2. Прочитать `projectbrief.md` и `systemPatterns.md`, если задача затрагивает архитектуру, интерфейсы или модули.
3. Проверить `progress.md` для понимания общего прогресса по проекту.

Или запустить быструю сводку:
```bash
python3 .agents/skills/memory-bank/scripts/memory_bank.py summary
```

## Шаг 2: Выполнение задачи
- Вносить изменения строго с учетом архитектурных соглашений из `systemPatterns.md` (Go 1.27.4, чистая стандартная библиотека, `encoding/json/v2`, HTMX Zero-Build, атомарная запись `state.json`, нормализация Git).

## Шаг 3: Синхронизация изменений (Context Writeback)
Перед завершением ответа пользователю:
1. Зафиксировать выполненную работу в `activeContext.md`:
   - Описание того, что было сделано.
   - Текущее состояние.
   - Следующие шаги.
2. Отметить выполненные пункты в `progress.md`.
3. При необходимости зафиксировать новые решения в `systemPatterns.md`.

## Шаг 4: Проверка целостности Memory Bank
Убедиться, что файлы не повреждены:
```bash
python3 .agents/skills/memory-bank/scripts/memory_bank.py validate
```
