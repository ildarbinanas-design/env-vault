# Tasks

Задачи реализации согласованного изменения. Отметки отражают выполненную
работу; результаты локальных проверок приведены ниже.
Контракт: [spec.md](specs/exec-result-reporting/spec.md);
решение и допущения: [design.md](design.md).

## 1. Успешный exec и границы обработки ошибок

- [x] 1.1 Добавить переносимую CLI-регрессию в отдельном файле без Unix build tag: child через `os.Args[0]` пишет публичные маркеры потоков и дописывает маркер запуска, завершается с `0`; `--output` указывает на временный каталог. Проверить исходное падение ожидания `exit 0`, единственный запуск, матрицу quiet/verbose и сохранение потоков.
- [x] 1.2 Добавить renderer-тесты success envelope при доступном/недоступном файле, human/JSON/JSONL и quiet/verbose, включая ошибку guard и счётчик его вызовов. Проверять отсутствие новых полей/warnings, одну публикацию при исправном stdout, точную verbose-диагностику и возврат ошибки failing stdout writer.
- [x] 1.3 Реализовать `CommandSucceeded` и общий закрытый `writeSuccess` по design; заменить только последний вызов рендерера реального успешного `exec`. Проверить зелёный результат `GOTOOLCHAIN=go1.26.8 go test ./internal/output ./internal/cli ./internal/runner` и неизменность общего `Success` для остальных вызовов.
- [x] 1.4 Добавить CLI-проверки защищённых путей до запуска и нового hard-link алиаса временной конфигурации после запуска. До запуска ожидать `USAGE`/`2` и отсутствие маркера; после запуска — `0`, неизменённые байты и файловый алиас, диагностику только с verbose. Недоступность hard links отмечать отдельным skip, не успешной проверкой.
- [x] 1.5 Покрыть остальные границы: child `7`, Unix SIGTERM с недоступным output, отсутствующий executable, `version` и `exec --dry-run` с output-каталогом, молчащий child с failing stdout writer для JSON/JSONL. Проверить прежние статусы/сигналы, отсутствие запуска в dry-run и код `1` при ошибке собственного stdout; сохранить имеющиеся проверки runner errors.
- [x] 1.6 Обновить [README](../../../README.md#output-and-dry-run) и [дизайн вывода](../../../docs/design.md#output-schema): нефатальный post-exec отказ файла, verbose-диагностика, риск устаревшей записи и строгая обработка ошибок вне этой границы. Проверить соответствие формулировок всем шести требованиям delta spec, без новых флагов и схем.

## 2. Проверка через собранный CLI

- [x] 2.1 Расширить существующий E2E `OUTPUT_JSON_JSONL_FILE`: успешный helper `streams` с недоступным output, JSON/JSONL и quiet/verbose. Проверить `0`, потоки, success envelope и отсутствие sentinel-значений; сохранить нынешний сценарий `version` с `RUNTIME_ERROR`/`1`.
- [x] 2.2 Собрать текущий CLI и выполнить целевой E2E-прогон ниже. Зафиксировать результат каждого выбранного сценария, включая явные платформенные skips; отсутствие `ENV_VAULT_E2E_BINARY` не считать прогоном E2E.

Команды для локального прогона на macOS/Linux из корня репозитория:

```sh
check_dir="$(mktemp -d)"
GOTOOLCHAIN=go1.26.8 go build -trimpath -o "$check_dir/env-vault" ./cmd/env-vault
ENV_VAULT_E2E_BINARY="$check_dir/env-vault" GOTOOLCHAIN=go1.26.8 \
  go test -count=1 \
  -run '^TestE2E$/(OUTPUT_JSON_JSONL_FILE|EXEC_ARG_STREAM_EXIT|EXEC_SIGNAL_FORWARDING|DRY_RUN_NO_SIDE_EFFECTS)$' \
  ./e2e
```

При ограничениях доступа к Go cache задать `GOCACHE` в system temp.
Harness сам собирает helper и изолирует HOME/TMP и все три test-backend gate.
Это прямой диагностический прогон из [docs/e2e.md](../../../docs/e2e.md),
не замена полного E2E runner или native CI-матрицы.

## 3. Интеграционные проверки и независимое ревью

- [x] 3.1 Запустить применимые проверки из [CONTRIBUTING](../../../CONTRIBUTING.md): формат изменённых Go-файлов, `git diff --check`, `GOTOOLCHAIN=go1.26.8 go test ./...`, `go vet ./...`, `go test -race ./...` и `scripts/smoke.sh` с тем же `GOTOOLCHAIN`. Зафиксировать команды, результаты и skips; при сбоях исправить реализацию и повторить затронутые проверки.
- [x] 3.2 Повторить строгую валидацию `openspec validate preserve-exec-success-on-metadata-write-failure --type change --strict --no-interactive --json` и `openspec validate --all --strict --no-interactive --json`; сверить требования, сценарии и завершённые задачи с фактическим diff, получить результат без ошибок и предупреждений.
- [x] 3.3 Передать окончательный diff и результаты независимому reviewer без истории реализации; устранить блокирующие замечания и повторить review после существенных исправлений. В отчёте перечислить фактически проверенные ОС, отдельно непроверенные Linux/Windows или результаты уже авторизованной native CI-матрицы, не расширяя самостоятельно объём доставки.

## Результаты локальных проверок

Платформа: macOS arm64; Go `1.26.8`, OpenSpec `1.14.0`.
Go-команды выполнялись с `GOTOOLCHAIN=go1.26.8` и `GOCACHE` в system temp.

| Проверка | Результат |
| --- | --- |
| Регрессия до исправления | Все четыре quiet/verbose-варианта воспроизвели ошибку: child завершился успешно, env-vault вернул `1` |
| `go test ./internal/output ./internal/cli ./internal/runner` | PASS после исправления |
| Новые CLI-проверки путей, child `7`, SIGTERM, dry-run, отсутствующего executable и stdout | PASS; hard-link проверки выполнены без skips |
| Собранный CLI: `OUTPUT_JSON_JSONL_FILE`, `EXEC_ARG_STREAM_EXIT`, `EXEC_SIGNAL_FORWARDING`, `DRY_RUN_NO_SIDE_EFFECTS` | Все четыре E2E-сценария PASS, без skips |
| `go test ./...` и `go test -race ./...` | PASS с оговорёнными ниже skips |
| `go vet ./...` | PASS |
| `scripts/smoke.sh` | `smoke ok`, exit `0` |
| Формат изменённых Go-файлов и `git diff --check` | PASS |
| Строгая валидация текущего change и `--all` | `valid: true`, `issues: []` |

После расширения E2E обновлён ожидаемый semantic suite hash в
`internal/e2esuite/hash_test.go`; правила вычисления хеша не менялись.
Первый полный прогон выявил прежнее значение, повторные обычный и race-прогоны
с обновлённым значением прошли.

Независимое ревью выявило нестабильность новых E2E-отчётов на Windows:
экранированный JSON-путь helper в смешанном child/JSON stdout не заменялся
стабильным placeholder. В новых сценариях helper передаётся с slash-разделителями;
добавлена проверка `argv[0] == "<TEST_HELPER>"` после нормализации отчёта.
Общий harness и правила нормализации не менялись.
Повторные четыре E2E-сценария с сохранением contracts прошли; в отчётах
проверены все 12 новых сочетаний режимов и восемь JSON/JSONL envelope.
Повторное независимое ревью окончательного diff и результатов проверок
не выявило оставшихся замечаний.

В обычном `go test ./...` пропущены `TestE2E` без `ENV_VAULT_E2E_BINARY`
(четыре выбранных сценария отдельно проверены на собранном CLI),
`TestHistoricalTransfer` без закреплённого исторического CLI, пять проверок
release steps без GNU `base64` и две проверки упаковки без GNU `tar`.
Полный E2E runner, native Linux/Windows и полный набор release-quality checks
не запускались; локальный результат не подтверждает их прохождение.
