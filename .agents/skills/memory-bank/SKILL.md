---
name: memory-bank
description: >-
  Use this skill to read, verify, initialize, or update the project context and memory bank in ./.agents/memory-bank.
  Activate when starting a new task to retrieve context, or after completing changes to record progress, decisions, and active state.
---

# Memory Bank Management Skill

Этот навык обучает агента работе с постоянным контекстом проекта, размещенным в `./.agents/memory-bank`.

## Быстрые действия

### 1. Чтение контекста (Context Ingestion)
Перед началом работы над задачей:
- **Быстрая сводка:** Запусти скрипт для получения сжатой информации без избыточного расхода контекстного окна:
  ```bash
  python3 .agents/skills/memory-bank/scripts/memory_bank.py summary
  ```
- **Детальное чтение:** При необходимости прочитай файлы напрямую:
  - `activeContext.md` — текущий фокус и последние изменения.
  - `systemPatterns.md` — архитектурные правила и ограничения.
  - `techContext.md` — версии и окружение.

### 2. Запись и обновление контекста (Context Synchronization)
После внесения изменений в код или завершения задачи:
1. Зафиксируй изменения в `activeContext.md`:
   - Либо отредактируй файл напрямую.
   - Либо добавь заметку через скрипт:
     ```bash
     python3 .agents/skills/memory-bank/scripts/memory_bank.py update-active "Краткое описание выполненных изменений"
     ```
2. Обнови `progress.md`:
   - Отметь завершенные пункты (`[x]`).
   - Добавь новые возникшие вопросы или задачи.
3. Если изменилась архитектура, стек или зависимости:
   - Добавь описание в `systemPatterns.md` или `techContext.md`.

### 3. Валидация состояния Memory Bank
Для проверки целостности структуры файлов контекста:
```bash
python3 .agents/skills/memory-bank/scripts/memory_bank.py validate
```

Подробную структуру файлов и их назначение см. в [references/structure.md](./references/structure.md).
