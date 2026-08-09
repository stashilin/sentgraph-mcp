package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	zep "github.com/getzep/zep-go/v3"
)

const dayJSONL = `{"ts":"2026-08-09T09:00:00Z","kind":"audio","speaker":"Стас","text":"Начинаем разбор бэклога."}
{"ts":"2026-08-09T09:00:30Z","kind":"audio","speaker":"Ира","text":"Первым идёт перелив памяти."}
{"ts":"2026-08-09T09:40:00Z","source":"screen","app":"Chrome","title":"Zep Docs","ocr":"Batch ingestion limits"}
{"created_at":"2026-08-09T09:41:00Z","source":"screen","app":"Chrome","title":"Zep Docs","ocr":"350 items per add call"}
`

func writeDay(t *testing.T, body string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "2026-08-09.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func dayOptions(src string) options {
	return options{
		kind:      "day",
		src:       src,
		dayExts:   ".jsonl,.ndjson",
		chunkSize: 1200,
		gap:       5 * time.Minute,
		sourceTag: "day",
	}
}

func TestLoadDaySplitsScenesAndPicksType(t *testing.T) {
	_, path := writeDay(t, dayJSONL)
	man := &manifestFile{Files: map[string]manifestDoc{}}

	episodes, planned, skipped, err := loadDay(dayOptions(path), man)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, ожидалось 0", skipped)
	}
	// Разные сцены (audio-разговор и экран Chrome) не должны слипнуться,
	// а 40-минутный разрыв закрывает эпизод в любом случае.
	if len(episodes) != 2 {
		t.Fatalf("эпизодов %d, ожидалось 2: %+v", len(episodes), episodes)
	}
	if episodes[0].Type != zep.GraphDataTypeMessage {
		t.Errorf("реплики со speaker должны быть message, получено %s", episodes[0].Type)
	}
	if episodes[1].Type != zep.GraphDataTypeText {
		t.Errorf("экран без speaker должен быть text, получено %s", episodes[1].Type)
	}
	if !strings.Contains(episodes[0].Data, "Стас: Начинаем разбор бэклога.") {
		t.Errorf("реплика потеряла говорящего:\n%s", episodes[0].Data)
	}
	if !strings.Contains(episodes[1].Data, "350 items per add call") {
		t.Errorf("текст экрана не попал в эпизод:\n%s", episodes[1].Data)
	}
	if episodes[0].CreatedAt != "2026-08-09T09:00:00Z" {
		t.Errorf("created_at = %q, ожидалось время первого события", episodes[0].CreatedAt)
	}
	rel := filepath.Base(path)
	if got := planned[rel].LastEventAt; got != "2026-08-09T09:41:00Z" {
		t.Errorf("last_event_at = %q, ожидалось время последнего события", got)
	}
}

func TestLoadDayTakesOnlyFreshEvents(t *testing.T) {
	_, path := writeDay(t, dayJSONL)
	rel := filepath.Base(path)
	man := &manifestFile{Files: map[string]manifestDoc{
		rel: {LastEventAt: "2026-08-09T09:00:30Z", Status: "submitted"},
	}}

	episodes, _, _, err := loadDay(dayOptions(path), man)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 {
		t.Fatalf("эпизодов %d, ожидался 1 (только события после last_event_at)", len(episodes))
	}
	if strings.Contains(episodes[0].Data, "Начинаем разбор") {
		t.Error("в дозаливку попали уже загруженные события — будут дубли")
	}
}

func TestLoadDaySkipsFullyLoadedFile(t *testing.T) {
	_, path := writeDay(t, dayJSONL)
	rel := filepath.Base(path)
	man := &manifestFile{Files: map[string]manifestDoc{
		rel: {LastEventAt: "2026-08-09T09:41:00Z", Status: "submitted"},
	}}

	episodes, _, skipped, err := loadDay(dayOptions(path), man)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 0 || skipped != 1 {
		t.Fatalf("эпизодов %d, skipped %d — ожидалось 0 и 1", len(episodes), skipped)
	}
}

func TestGroupDayRespectsChunkSize(t *testing.T) {
	base := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	var events []dayEvent
	for i := 0; i < 20; i++ {
		events = append(events, dayEvent{
			TS:   base.Add(time.Duration(i) * time.Second),
			Kind: "screen",
			App:  "Chrome",
			Text: strings.Repeat("текст ", 20),
		})
	}
	for _, g := range groupDay(events, 300, time.Minute) {
		total := 0
		for _, e := range g.events {
			total += runeLen(renderEvent(e)) + 1
		}
		if total > 300 && len(g.events) > 1 {
			t.Errorf("группа из %d событий набрала %d символов при лимите 300", len(g.events), total)
		}
	}
}
