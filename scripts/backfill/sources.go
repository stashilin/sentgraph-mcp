package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	zep "github.com/getzep/zep-go/v3"
)

// episode — то, что уедет в Zep одним элементом батча.
type episode struct {
	Data      string
	Type      zep.GraphDataType
	CreatedAt string // RFC3339
	Source    string // ключ манифеста: относительный путь файла
	Label     string // человекочитаемая подпись для превью
	Meta      map[string]interface{}
	EventAt   time.Time // для инкрементальной дозаливки дня
}

type srcFile struct {
	path    string
	rel     string
	modTime time.Time
}

func collectFiles(root, exts string) ([]srcFile, error) {
	allowed := map[string]bool{}
	for _, e := range strings.Split(exts, ",") {
		e = strings.TrimSpace(e)
		if e != "" {
			allowed[strings.ToLower(e)] = true
		}
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []srcFile{{path: root, rel: filepath.Base(root), modTime: info.ModTime()}}, nil
	}

	var out []srcFile
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return fs.SkipDir
			}
			return nil
		}
		if !allowed[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		out = append(out, srcFile{path: path, rel: filepath.ToSlash(rel), modTime: fi.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

// loadDocs: документы → текстовые эпизоды с цепочкой заголовков в контексте.
// Файл целиком пропускается, если его sha256 уже записан в манифесте.
func loadDocs(opt options, man *manifestFile) ([]episode, map[string]manifestDoc, int, error) {
	files, err := collectFiles(opt.src, opt.exts)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(files) == 0 {
		return nil, nil, 0, fmt.Errorf("в %s нет файлов с расширениями %s", opt.src, opt.exts)
	}

	var out []episode
	planned := map[string]manifestDoc{}
	skipped := 0

	for _, f := range files {
		raw, err := os.ReadFile(f.path)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("чтение %s: %w", f.path, err)
		}
		sum := sha256.Sum256(raw)
		sha := hex.EncodeToString(sum[:])
		if prev, ok := man.Files[f.rel]; ok && prev.SHA == sha {
			skipped++
			continue
		}

		parts := chunkMarkdown(string(raw), opt.chunkSize, opt.overlap)
		if len(parts) == 0 {
			continue
		}
		created := f.modTime.UTC().Format(time.RFC3339)
		for i, p := range parts {
			head := f.rel
			if p.heading != "" {
				head += " › " + p.heading
			}
			out = append(out, episode{
				Data:      "[" + head + "]\n\n" + p.text,
				Type:      zep.GraphDataTypeText,
				CreatedAt: created,
				Source:    f.rel,
				Label:     fmt.Sprintf("%s [%d/%d]", f.rel, i+1, len(parts)),
				Meta: map[string]interface{}{
					"source":      opt.sourceTag,
					"path":        f.rel,
					"doc":         strings.TrimSuffix(filepath.Base(f.rel), filepath.Ext(f.rel)),
					"chunk":       i,
					"chunks":      len(parts),
					"content_sha": sha[:12],
				},
			})
		}
		planned[f.rel] = manifestDoc{SHA: sha, Chunks: len(parts), Status: "submitted"}
	}
	return out, planned, skipped, nil
}

// dayEvent — одна запись дня: реплика из аудио, кусок распознанного экрана, заметка.
type dayEvent struct {
	TS      time.Time
	Kind    string // audio | screen | note
	Speaker string
	App     string
	Title   string
	Text    string
}

// rawEvent принимает разные имена полей: у каждого рекордера они свои,
// а заводить по конвертеру на каждый — дороже, чем принять синонимы здесь.
type rawEvent struct {
	TS        string `json:"ts"`
	Time      string `json:"time"`
	CreatedAt string `json:"created_at"`
	Start     string `json:"start"`

	Text       string `json:"text"`
	Content    string `json:"content"`
	Transcript string `json:"transcript"`
	OCR        string `json:"ocr"`

	Kind   string `json:"kind"`
	Source string `json:"source"`
	Type   string `json:"type"`

	Speaker string `json:"speaker"`
	Name    string `json:"name"`

	App         string `json:"app"`
	Application string `json:"application"`
	Title       string `json:"title"`
	Window      string `json:"window"`
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func parseTS(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("не разобрал время %q", s)
}

func readDayFile(path string) ([]dayEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []dayEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" {
			continue
		}
		var raw rawEvent
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		text := firstNonEmpty(raw.Text, raw.Content, raw.Transcript, raw.OCR)
		if text == "" {
			continue
		}
		tsRaw := firstNonEmpty(raw.TS, raw.Time, raw.CreatedAt, raw.Start)
		if tsRaw == "" {
			return nil, fmt.Errorf("%s:%d: нет времени события", path, line)
		}
		ts, err := parseTS(tsRaw)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, dayEvent{
			TS:      ts.UTC(),
			Kind:    strings.ToLower(firstNonEmpty(raw.Kind, raw.Source, raw.Type, "note")),
			Speaker: firstNonEmpty(raw.Speaker, raw.Name),
			App:     firstNonEmpty(raw.App, raw.Application),
			Title:   firstNonEmpty(raw.Title, raw.Window),
			Text:    text,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out, nil
}

// loadDay: JSONL-поток дня → эпизоды. События склеиваются в один эпизод, пока
// не сменилась сцена (kind/app/title), не разошлись во времени больше -gap и
// не набрался -chunk символов. Повторный прогон берёт только то, что новее
// last_event_at из манифеста: день дописывается, а дублей быть не должно.
func loadDay(opt options, man *manifestFile) ([]episode, map[string]manifestDoc, int, error) {
	files, err := collectFiles(opt.src, opt.dayExts)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(files) == 0 {
		return nil, nil, 0, fmt.Errorf("в %s нет файлов с расширениями %s", opt.src, opt.dayExts)
	}

	var out []episode
	planned := map[string]manifestDoc{}
	skipped := 0

	for _, f := range files {
		events, err := readDayFile(f.path)
		if err != nil {
			return nil, nil, 0, err
		}
		if len(events) == 0 {
			continue
		}

		var after time.Time
		if prev, ok := man.Files[f.rel]; ok && prev.LastEventAt != "" {
			if t, err := parseTS(prev.LastEventAt); err == nil {
				after = t
			}
		}
		fresh := events[:0:0]
		for _, e := range events {
			if !after.IsZero() && !e.TS.After(after) {
				continue
			}
			fresh = append(fresh, e)
		}
		if len(fresh) == 0 {
			skipped++
			continue
		}

		groups := groupDay(fresh, opt.chunkSize, opt.gap)
		last := fresh[len(fresh)-1].TS
		for i, g := range groups {
			out = append(out, g.toEpisode(opt, f.rel, i, len(groups)))
		}
		planned[f.rel] = manifestDoc{
			Chunks:      len(groups),
			LastEventAt: last.UTC().Format(time.RFC3339),
			Status:      "submitted",
		}
	}
	return out, planned, skipped, nil
}

type dayGroup struct {
	events []dayEvent
	kind   string
	app    string
	title  string
}

func scene(e dayEvent) [3]string { return [3]string{e.Kind, e.App, e.Title} }

func groupDay(events []dayEvent, size int, gap time.Duration) []dayGroup {
	var out []dayGroup
	var cur dayGroup
	curLen := 0

	flush := func() {
		if len(cur.events) > 0 {
			out = append(out, cur)
		}
		cur, curLen = dayGroup{}, 0
	}

	for _, e := range events {
		line := renderEvent(e)
		newScene := len(cur.events) > 0 && scene(cur.events[len(cur.events)-1]) != scene(e)
		tooLate := len(cur.events) > 0 && e.TS.Sub(cur.events[len(cur.events)-1].TS) > gap
		tooBig := curLen+runeLen(line)+1 > size && len(cur.events) > 0
		if newScene || tooLate || tooBig {
			flush()
		}
		if len(cur.events) == 0 {
			cur.kind, cur.app, cur.title = e.Kind, e.App, e.Title
		}
		cur.events = append(cur.events, e)
		curLen += runeLen(line) + 1
	}
	flush()
	return out
}

func renderEvent(e dayEvent) string {
	if e.Speaker != "" {
		return e.Speaker + ": " + e.Text
	}
	return e.Text
}

func (g dayGroup) toEpisode(opt options, rel string, idx, total int) episode {
	first := g.events[0].TS
	last := g.events[len(g.events)-1].TS

	scene := g.kind
	if g.app != "" {
		scene += " · " + g.app
	}
	if g.title != "" {
		scene += ": " + g.title
	}
	head := fmt.Sprintf("%s %s–%s · %s",
		first.Local().Format("2006-01-02"),
		first.Local().Format("15:04"),
		last.Local().Format("15:04"),
		scene)

	lines := make([]string, 0, len(g.events))
	speakers := map[string]bool{}
	for _, e := range g.events {
		lines = append(lines, renderEvent(e))
		if e.Speaker != "" {
			speakers[e.Speaker] = true
		}
	}

	// Реплики с именами говорящих — это message в терминах Zep; поток без
	// speaker (распознанный экран, заметки) остаётся text.
	dataType := zep.GraphDataTypeText
	if len(speakers) > 0 {
		dataType = zep.GraphDataTypeMessage
	}

	meta := map[string]interface{}{
		"source":   opt.sourceTag,
		"kind":     g.kind,
		"date":     first.Local().Format("2006-01-02"),
		"path":     rel,
		"events":   len(g.events),
		"span_min": int(last.Sub(first).Minutes()),
	}
	if g.app != "" {
		meta["app"] = g.app
	}
	if g.title != "" {
		meta["title"] = g.title
	}
	if len(speakers) > 0 {
		names := make([]string, 0, len(speakers))
		for s := range speakers {
			names = append(names, s)
		}
		sort.Strings(names)
		meta["speakers"] = names
	}

	return episode{
		Data:      "[" + head + "]\n\n" + strings.Join(lines, "\n"),
		Type:      dataType,
		CreatedAt: first.UTC().Format(time.RFC3339),
		Source:    rel,
		Label:     fmt.Sprintf("%s [%d/%d] %s", rel, idx+1, total, head),
		Meta:      meta,
		EventAt:   last,
	}
}
