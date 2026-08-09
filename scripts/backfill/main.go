// Command backfill переливает историю в память Zep через Batch API.
//
// Источники (-kind):
//
//	day   — JSONL-поток дня: транскрипты аудио, распознанный экран, заметки
//	docs  — каталог markdown/текста
//
// По умолчанию dry-run: считает эпизоды и символы, пишет превью, ничего не шлёт.
// Отправка — только с -apply. Весь текст перед отправкой проходит через
// internal/redact, как и обычная запись памяти sentgraph-mcp.
//
//	go run ./scripts/backfill -kind day -src ~/data/day/2026-08-09.jsonl -user stas
//	go run ./scripts/backfill -kind day -src ~/data/day/2026-08-09.jsonl -user stas -apply
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	zep "github.com/getzep/zep-go/v3"
	zepclient "github.com/getzep/zep-go/v3/client"
	"github.com/getzep/zep-go/v3/option"

	"github.com/shilin23061991/sentgraph-mcp/internal/redact"
)

const (
	// Жёсткий лимит Zep на размер эпизода: больше — 400, а не обрезка на их стороне.
	maxEpisodeChars = 10000
	// Лимиты Batch API: items за один batch.add и на батч целиком.
	maxItemsPerAddCall = 350
	maxItemsPerBatch   = 50000
	// Запас под строку контекста, которую скрипт приписывает каждому эпизоду.
	headroomChars = 1000
)

type options struct {
	kind       string
	src        string
	exts       string
	dayExts    string
	graphID    string
	userID     string
	chunkSize  int
	overlap    int
	gap        time.Duration
	apply      bool
	manifest   string
	sourceTag  string
	pollEvery  time.Duration
	pollFor    time.Duration
	previewLen int
}

// manifestFile — что уже принято Zep. Zep не дедуплицирует эпизоды по содержимому,
// поэтому повторный прогон без манифеста создаст вторые копии.
type manifestFile struct {
	UpdatedAt string                 `json:"updated_at"`
	Target    string                 `json:"target"`
	Batches   []string               `json:"batch_ids"`
	Files     map[string]manifestDoc `json:"files"`
}

type manifestDoc struct {
	SHA         string `json:"sha256,omitempty"`
	LastEventAt string `json:"last_event_at,omitempty"`
	Chunks      int    `json:"chunks"`
	BatchID     string `json:"batch_id,omitempty"`
	Status      string `json:"status"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "backfill:", err)
		os.Exit(1)
	}
}

func run() error {
	var opt options
	flag.StringVar(&opt.kind, "kind", "day", "источник: day (JSONL аудио/экран/заметки) или docs (markdown)")
	flag.StringVar(&opt.src, "src", "", "файл или каталог с данными (обязательно)")
	flag.StringVar(&opt.exts, "ext", ".md,.txt,.mdx", "расширения для -kind docs")
	flag.StringVar(&opt.dayExts, "day-ext", ".jsonl,.ndjson", "расширения для -kind day")
	flag.StringVar(&opt.graphID, "graph", "", "graph_id назначения (например proj:sentgraph)")
	flag.StringVar(&opt.userID, "user", "", "user_id назначения вместо графа проекта")
	flag.IntVar(&opt.chunkSize, "chunk", 0, "размер эпизода в символах (0 — авто: 500 для docs, 1200 для day)")
	flag.IntVar(&opt.overlap, "overlap", 50, "перекрытие между чанками, только для -kind docs")
	flag.DurationVar(&opt.gap, "gap", 5*time.Minute, "разрыв во времени, закрывающий эпизод дня")
	flag.BoolVar(&opt.apply, "apply", false, "реально отправить в Zep (без флага — dry-run)")
	flag.StringVar(&opt.manifest, "manifest", "", "файл манифеста (по умолчанию .ingest/<kind>.json)")
	flag.StringVar(&opt.sourceTag, "source", "", "метка источника в метаданных (по умолчанию = -kind)")
	flag.DurationVar(&opt.pollEvery, "poll", 10*time.Second, "интервал опроса статуса батча")
	flag.DurationVar(&opt.pollFor, "timeout", 2*time.Hour, "предельное время ожидания обработки батча")
	flag.IntVar(&opt.previewLen, "preview", 5, "сколько эпизодов показать в dry-run")
	flag.Parse()

	if opt.src == "" {
		flag.Usage()
		return fmt.Errorf("не задан -src")
	}
	switch opt.kind {
	case "day", "docs":
	default:
		return fmt.Errorf("-kind должен быть day или docs, получено %q", opt.kind)
	}
	if opt.chunkSize == 0 {
		opt.chunkSize = map[string]int{"docs": 500, "day": 1200}[opt.kind]
	}
	// Верхняя граница ниже лимита Zep: к каждому эпизоду добавляется строка контекста.
	if opt.chunkSize < 100 || opt.chunkSize > maxEpisodeChars-headroomChars {
		return fmt.Errorf("-chunk должен быть в диапазоне 100..%d", maxEpisodeChars-headroomChars)
	}
	if opt.overlap < 0 || opt.overlap >= opt.chunkSize {
		return fmt.Errorf("-overlap должен быть меньше -chunk")
	}
	if opt.sourceTag == "" {
		opt.sourceTag = opt.kind
	}
	if opt.manifest == "" {
		opt.manifest = filepath.Join(".ingest", opt.kind+".json")
	}

	// .env.local подхватываем как и сам sentgraph-mcp: реальный env побеждает.
	_ = godotenv.Load(".env.local")

	if opt.graphID == "" && opt.userID == "" {
		if pid := strings.TrimSpace(os.Getenv("SENTGRAPH_PROJECT_ID")); pid != "" {
			opt.graphID = "proj:" + pid
		}
	}
	if (opt.graphID == "") == (opt.userID == "") {
		return fmt.Errorf("нужен ровно один из -graph / -user (или SENTGRAPH_PROJECT_ID в .env.local)")
	}

	target := opt.graphID
	if target == "" {
		target = "user:" + opt.userID
	}

	man, err := loadManifest(opt.manifest)
	if err != nil {
		return err
	}
	if man.Target != "" && man.Target != target {
		return fmt.Errorf("манифест %s принадлежит цели %q, а сейчас цель %q — возьми отдельный -manifest",
			opt.manifest, man.Target, target)
	}

	var (
		episodes []episode
		planned  map[string]manifestDoc
		skipped  int
	)
	if opt.kind == "docs" {
		episodes, planned, skipped, err = loadDocs(opt, man)
	} else {
		episodes, planned, skipped, err = loadDay(opt, man)
	}
	if err != nil {
		return err
	}

	// Редакция секретов — до любой отправки и до превью: с экрана и из речи
	// в память утекает больше всего.
	totalChars, redacted, truncated := 0, 0, 0
	for i := range episodes {
		clean := redact.Secrets(episodes[i].Data)
		if clean != episodes[i].Data {
			redacted++
		}
		if runeLen(clean) > maxEpisodeChars {
			clean, _ = runeCut(clean, maxEpisodeChars)
			truncated++
		}
		episodes[i].Data = clean
		totalChars += runeLen(clean)
	}

	fmt.Printf("источник:    %s (%s)\n", opt.src, opt.kind)
	fmt.Printf("цель:        %s\n", target)
	fmt.Printf("эпизодов:    %d (файлов пропущено как уже загруженные: %d)\n", len(episodes), skipped)
	fmt.Printf("символов:    %d\n", totalChars)
	if redacted > 0 {
		fmt.Printf("redact:      секреты вырезаны в %d эпизод(ах)\n", redacted)
	}
	if truncated > 0 {
		fmt.Printf("ВНИМАНИЕ:    %d эпизод(ов) обрезано по лимиту %d символов — уменьши -chunk\n", truncated, maxEpisodeChars)
	}
	if len(episodes) == 0 {
		fmt.Println("нечего загружать")
		return nil
	}
	fmt.Printf("batch.add:   %d вызов(ов) по %d items\n",
		(len(episodes)+maxItemsPerAddCall-1)/maxItemsPerAddCall, maxItemsPerAddCall)

	if !opt.apply {
		previewPath := strings.TrimSuffix(opt.manifest, filepath.Ext(opt.manifest)) + "-preview.md"
		if err := writePreview(previewPath, episodes, opt.previewLen); err != nil {
			return err
		}
		fmt.Printf("\nDRY-RUN. Превью: %s\n", previewPath)
		fmt.Printf("Отправка: повтори команду с -apply (будет создано %d эпизодов, они тарифицируются)\n", len(episodes))
		return nil
	}

	apiKey := strings.TrimSpace(os.Getenv("ZEP_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("ZEP_API_KEY не задан (запусти через pass-cli run или положи .env.local)")
	}
	client := zepclient.NewClient(option.WithAPIKey(apiKey))
	ctx := context.Background()

	if err := ensureTarget(ctx, client, opt); err != nil {
		return err
	}

	for start := 0; start < len(episodes); start += maxItemsPerBatch {
		end := min(start+maxItemsPerBatch, len(episodes))
		slice := episodes[start:end]

		// batch_id уходит в манифест сразу после создания батча: обрыв на любом
		// следующем шаге не должен лишить нас единственной ниточки к принятым данным.
		onCreated := func(batchID string) error {
			man.Batches = append(man.Batches, batchID)
			return saveManifest(opt.manifest, man, target)
		}

		batchID, err := ingestBatch(ctx, client, opt, target, slice, onCreated)
		if err != nil {
			// Судьба этих файлов неизвестна: часть эпизодов могла быть принята.
			// Отметку о прогрессе не пишем, чтобы следующий прогон их разобрал.
			for _, rel := range filesOf(slice) {
				man.Files[rel] = manifestDoc{
					Chunks:  planned[rel].Chunks,
					BatchID: batchID,
					Status:  "unknown",
				}
			}
			_ = saveManifest(opt.manifest, man, target)
			return err
		}
		for _, rel := range filesOf(slice) {
			doc := planned[rel]
			doc.BatchID = batchID
			man.Files[rel] = doc
		}
		if err := saveManifest(opt.manifest, man, target); err != nil {
			return err
		}
	}

	fmt.Printf("\nманифест: %s\n", opt.manifest)
	return nil
}

func ensureTarget(ctx context.Context, client *zepclient.Client, opt options) error {
	if opt.graphID != "" {
		if _, err := client.Graph.Get(ctx, opt.graphID); err != nil {
			return fmt.Errorf("граф %q недоступен (создай его заранее): %w", opt.graphID, err)
		}
		return nil
	}
	if _, err := client.User.Get(ctx, opt.userID); err != nil {
		return fmt.Errorf("пользователь %q недоступен (создай его заранее): %w", opt.userID, err)
	}
	return nil
}

// filesOf — уникальные исходные файлы, чьи эпизоды попали в этот срез.
func filesOf(episodes []episode) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range episodes {
		if !seen[e.Source] {
			seen[e.Source] = true
			out = append(out, e.Source)
		}
	}
	return out
}

func ingestBatch(ctx context.Context, client *zepclient.Client, opt options, target string, episodes []episode, onCreated func(string) error) (string, error) {
	created, err := client.Batch.Create(ctx, &zep.ApidataCreateBatchRequest{
		Metadata: map[string]interface{}{
			"description": fmt.Sprintf("backfill %s %s -> %s", opt.kind, filepath.Base(opt.src), target),
		},
	})
	if err != nil {
		return "", fmt.Errorf("batch.create: %w", err)
	}
	batchID := ""
	if created.BatchID != nil {
		batchID = *created.BatchID
	}
	if batchID == "" {
		return "", fmt.Errorf("batch.create вернул пустой batch_id")
	}
	if onCreated != nil {
		if err := onCreated(batchID); err != nil {
			return batchID, err
		}
	}
	fmt.Printf("\nbatch %s: заливаю %d эпизодов\n", batchID, len(episodes))

	for start := 0; start < len(episodes); start += maxItemsPerAddCall {
		end := min(start+maxItemsPerAddCall, len(episodes))
		items := make([]*zep.BatchAddItem, 0, end-start)
		for _, e := range episodes[start:end] {
			item := &zep.BatchAddItem{
				Type:              zep.ApidataBatchAddItemTypeGraphEpisode,
				Data:              zep.String(e.Data),
				DataType:          e.Type.Ptr(),
				CreatedAt:         zep.String(e.CreatedAt),
				SourceDescription: zep.String("backfill " + opt.kind + ": " + e.Source),
				Metadata:          e.Meta,
			}
			if opt.graphID != "" {
				item.GraphID = zep.String(opt.graphID)
			} else {
				item.UserID = zep.String(opt.userID)
			}
			items = append(items, item)
		}
		if _, err := client.Batch.Add(ctx, batchID, &zep.ApidataAddBatchItemsRequest{Items: items}); err != nil {
			return batchID, fmt.Errorf("batch.add (%d..%d): %w", start, end, err)
		}
		fmt.Printf("  добавлено %d/%d\n", end, len(episodes))
	}

	if _, err := client.Batch.Process(ctx, batchID); err != nil {
		return batchID, fmt.Errorf("batch.process: %w", err)
	}

	return batchID, waitBatch(ctx, client, batchID, opt.pollEvery, opt.pollFor)
}

func waitBatch(ctx context.Context, client *zepclient.Client, batchID string, every, limit time.Duration) error {
	terminal := map[zep.BatchStatus]bool{
		zep.BatchStatusSucceeded: true,
		zep.BatchStatusPartial:   true,
		zep.BatchStatusFailed:    true,
		zep.BatchStatusInvalid:   true,
		zep.BatchStatusCanceled:  true,
	}
	deadline := time.Now().Add(limit)
	for {
		summary, err := client.Batch.Get(ctx, batchID)
		if err != nil {
			return fmt.Errorf("batch.get: %w", err)
		}
		status := zep.BatchStatus("")
		if summary.Status != nil {
			status = *summary.Status
		}
		done, total := 0, 0
		if p := summary.Progress; p != nil {
			if p.SucceededItems != nil {
				done = *p.SucceededItems
			}
			if p.TotalItems != nil {
				total = *p.TotalItems
			}
		}
		fmt.Printf("  статус %s: %d/%d\n", status, done, total)
		if terminal[status] {
			if status == zep.BatchStatusSucceeded {
				return nil
			}
			return fmt.Errorf("батч %s завершился со статусом %s — разбирай через GET /batches/%s/items", batchID, status, batchID)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("батч %s не завершился за %s (данные уже приняты, проверь статус позже)", batchID, limit)
		}
		time.Sleep(every)
	}
}

func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func writePreview(path string, episodes []episode, n int) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# backfill preview\n\n")
	fmt.Fprintf(&b, "Всего эпизодов: %d\n\n", len(episodes))
	for i, e := range episodes {
		if i >= n {
			break
		}
		fmt.Fprintf(&b, "## %s — %s, %d символов\n\n```\n%s\n```\n\n", e.Label, e.Type, runeLen(e.Data), e.Data)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func loadManifest(path string) (*manifestFile, error) {
	man := &manifestFile{Files: map[string]manifestDoc{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return man, nil
	}
	if err != nil {
		return nil, fmt.Errorf("чтение манифеста %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, man); err != nil {
		return nil, fmt.Errorf("разбор манифеста %s: %w", path, err)
	}
	if man.Files == nil {
		man.Files = map[string]manifestDoc{}
	}
	return man, nil
}

func saveManifest(path string, man *manifestFile, target string) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	man.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	man.Target = target
	raw, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
