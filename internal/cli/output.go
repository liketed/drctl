package cli

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// output writes rows as an aligned table, CSV (with a header, re-importable
// with the matching import command) or JSON (an array of objects keyed by the
// CSV column names).
func output(w io.Writer, format string, header []string, rows [][]string) error {
	switch format {
	case "table", "":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, strings.ToUpper(strings.Join(header, "\t")))
		for _, r := range rows {
			fmt.Fprintln(tw, strings.Join(r, "\t"))
		}
		return tw.Flush()
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write(header)
		_ = cw.WriteAll(rows)
		return cw.Error()
	case "json":
		objs := make([]map[string]string, 0, len(rows))
		for _, r := range rows {
			o := map[string]string{}
			for i, h := range header {
				o[h] = r[i]
			}
			objs = append(objs, o)
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(objs)
	}
	return usagef("unknown --format %q (use table, csv or json)", format)
}

// csvLine is one data line of an import file.
type csvLine struct {
	num    int
	fields []string
}

// readCSV reads an import file ("-" for stdin), skipping blank lines and lines
// starting with "#". Fields are trimmed. If the first data line starts with
// one of headerFirst (case-insensitive), it is returned as the header.
func readCSV(path string, stdin io.Reader, headerFirst string) (header []string, lines []csvLine, err error) {
	var r io.Reader
	if path == "-" {
		r = stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			var pe *os.PathError
			if errors.As(err, &pe) {
				return nil, nil, fmt.Errorf("cannot read %s: %v", path, pe.Err)
			}
			return nil, nil, err
		}
		defer f.Close()
		r = f
	}
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = true
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %v", path, err)
		}
		line, _ := cr.FieldPos(0)
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		if strings.Join(rec, "") == "" || strings.HasPrefix(rec[0], "#") {
			continue
		}
		if header == nil && len(lines) == 0 && strings.EqualFold(rec[0], headerFirst) {
			for i := range rec {
				rec[i] = strings.ToLower(rec[i])
			}
			header = rec
			continue
		}
		lines = append(lines, csvLine{num: line, fields: rec})
	}
	if len(lines) == 0 {
		return nil, nil, fmt.Errorf("%s: no records found", path)
	}
	return header, lines, nil
}

// column returns a line's value for a header column, or "".
func (l csvLine) column(header []string, name string) string {
	for i, h := range header {
		if h == name && i < len(l.fields) {
			return l.fields[i]
		}
	}
	return ""
}

func lineErr(path string, l csvLine, format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", path, l.num, fmt.Sprintf(format, args...))
}
