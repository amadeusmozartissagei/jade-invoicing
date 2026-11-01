// Command runsql runs a SQL file or a single query against DATABASE_URL.
//
// It reads variables from the repository's .env.local (without printing
// them) and replaces ${NAME} placeholders in the SQL, so secrets such as the
// spike token never have to be written into a committed file.
//
//	go run ./cmd/runsql -f sql/setup.sql
//	go run ./cmd/runsql -c "select count(*) from pgmq.q_spike"
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	file := flag.String("f", "", "SQL file to execute")
	query := flag.String("c", "", "single query; rows are printed")
	envFile := flag.String("env", "../../.env.local", "env file with DATABASE_URL")
	flag.Parse()

	if err := loadEnv(*envFile); err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := pgx.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		fail(fmt.Errorf("parse DATABASE_URL: %w", err))
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		fail(err)
	}
	defer conn.Close(ctx)

	switch {
	case *file != "":
		b, err := os.ReadFile(*file)
		if err != nil {
			fail(err)
		}
		tag, err := conn.Exec(ctx, expand(string(b)))
		if err != nil {
			fail(err)
		}
		fmt.Println("ok:", tag.String())
	case *query != "":
		rows, err := conn.Query(ctx, expand(*query))
		if err != nil {
			fail(err)
		}
		defer rows.Close()
		var cols []string
		for _, f := range rows.FieldDescriptions() {
			cols = append(cols, f.Name)
		}
		fmt.Println(strings.Join(cols, " | "))
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				fail(err)
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = fmt.Sprint(v)
			}
			fmt.Println(strings.Join(parts, " | "))
		}
		if err := rows.Err(); err != nil {
			fail(err)
		}
	default:
		fail(fmt.Errorf("use -f FILE or -c QUERY"))
	}
}

var placeholder = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

func expand(sql string) string {
	return placeholder.ReplaceAllStringFunc(sql, func(m string) string {
		name := placeholder.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			fail(fmt.Errorf("variable %s is not set", name))
		}
		return v
	})
}

func loadEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(strings.TrimSpace(k)); !set {
			os.Setenv(strings.TrimSpace(k), v)
		}
	}
	return s.Err()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
