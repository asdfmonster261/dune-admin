package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Minimal INI editor that preserves comments + section ordering. The .ini
// files we touch (UserEngine.ini, director.ini) are not strict INI; they
// allow ; comments mid-section, repeated section headers in some files,
// and values can contain quoted text. Good enough for our edit needs:
// look up by [section] + key, replace value in-place, preserve every
// other byte.

type iniLine struct {
	raw     string
	section string // populated for [section] lines
	key     string // populated for key=value lines
	rawKV   bool
}

type iniFile struct {
	path  string
	lines []iniLine
}

func readINIFile(path string) (*iniFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := &iniFile{path: path}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	currentSection := ""
	for scanner.Scan() {
		raw := scanner.Text()
		trim := strings.TrimSpace(raw)

		// Section header — strip surrounding [] (and optional spaces inside).
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			currentSection = strings.TrimSpace(trim[1 : len(trim)-1])
			out.lines = append(out.lines, iniLine{
				raw:     raw,
				section: currentSection,
			})
			continue
		}
		// Comment or blank.
		if trim == "" || strings.HasPrefix(trim, ";") || strings.HasPrefix(trim, "#") {
			out.lines = append(out.lines, iniLine{raw: raw})
			continue
		}
		// Key=value (or key=value with trailing comment, which we just leave alone).
		k, _, ok := strings.Cut(trim, "=")
		if !ok {
			out.lines = append(out.lines, iniLine{raw: raw})
			continue
		}
		k = strings.TrimSpace(k)
		out.lines = append(out.lines, iniLine{
			raw:     raw,
			section: currentSection,
			key:     k,
			rawKV:   true,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *iniFile) get(section, key string) (string, bool) {
	for _, l := range f.lines {
		if !l.rawKV || l.section != section || l.key != key {
			continue
		}
		_, v, _ := strings.Cut(l.raw, "=")
		return strings.TrimSpace(v), true
	}
	return "", false
}

// set replaces the value of [section].key in place. Preserves leading
// whitespace + the existing inline comment (if any) after the value.
// Returns false if the key was not found under that section.
func (f *iniFile) set(section, key, value string) bool {
	for i, l := range f.lines {
		if !l.rawKV || l.section != section || l.key != key {
			continue
		}
		// Preserve original leading whitespace (used to keep semi-colon-
		// commented-out lines indented).
		leading := l.raw[:len(l.raw)-len(strings.TrimLeft(l.raw, " \t"))]
		f.lines[i].raw = fmt.Sprintf("%s%s=%s", leading, key, value)
		return true
	}
	return false
}

func (f *iniFile) save() error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(f.path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := f.path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(out)
	for _, l := range f.lines {
		w.WriteString(l.raw)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, f.path)
}
