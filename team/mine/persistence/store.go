package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openegiz-contest/team-050/team/mine/domain"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB   *sql.DB
	Lock *sql.DB
	Dir  string
}

func Open(dir string, lock bool) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	s := &Store{Dir: dir}
	var e error
	if lock {
		s.Lock, e = sql.Open("sqlite", filepath.Join(dir, "engine-owner.db"))
		if e != nil {
			return nil, e
		}
		s.Lock.SetMaxOpenConns(1)
		if _, e = s.Lock.Exec("BEGIN EXCLUSIVE"); e != nil {
			s.Lock.Close()
			return nil, fmt.Errorf("another engine owns this directory: %w", e)
		}
	}
	s.DB, e = sql.Open("sqlite", filepath.Join(dir, "mine.db"))
	if e != nil {
		s.Close()
		return nil, e
	}
	s.DB.SetMaxOpenConns(1)
	_, e = s.DB.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; CREATE TABLE IF NOT EXISTS snapshot(id INTEGER PRIMARY KEY CHECK(id=1),body BLOB NOT NULL); CREATE TABLE IF NOT EXISTS commands(run_id TEXT,command_id TEXT,request BLOB NOT NULL,result BLOB NOT NULL,PRIMARY KEY(run_id,command_id)); CREATE TABLE IF NOT EXISTS runs(run_id TEXT PRIMARY KEY,body BLOB NOT NULL); CREATE TABLE IF NOT EXISTS events(run_id TEXT,event_id INTEGER,body BLOB NOT NULL,PRIMARY KEY(run_id,event_id));`)
	if e != nil {
		s.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() {
	if s.DB != nil {
		s.DB.Close()
	}
	if s.Lock != nil {
		s.Lock.Exec("ROLLBACK")
		s.Lock.Close()
	}
}
func (s *Store) Load() (*domain.State, error) {
	var b []byte
	e := s.DB.QueryRow("SELECT body FROM snapshot WHERE id=1").Scan(&b)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var v domain.State
	e = json.Unmarshal(b, &v)
	return &v, e
}
func (s *Store) Find(run, id string) ([]byte, *domain.Result, error) {
	var req, b []byte
	e := s.DB.QueryRow("SELECT request,result FROM commands WHERE run_id=? AND command_id=?", run, id).Scan(&req, &b)
	if e == sql.ErrNoRows {
		return nil, nil, nil
	}
	if e != nil {
		return nil, nil, e
	}
	var r domain.Result
	e = json.Unmarshal(b, &r)
	return req, &r, e
}
func (s *Store) Save(v *domain.State, cmd *domain.Command, results []domain.Result) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT OR REPLACE INTO snapshot VALUES(1,?)", b); e != nil {
		return e
	}
	for _, r := range results {
		rb, _ := json.Marshal(r)
		if cmd != nil && r.ID == cmd.ID && r.RunID == cmd.RunID {
			request, _ := json.Marshal(cmd)
			_, e = tx.Exec("INSERT OR REPLACE INTO commands VALUES(?,?,?,?)", r.RunID, r.ID, request, rb)
		} else {
			_, e = tx.Exec("UPDATE commands SET result=? WHERE run_id=? AND command_id=?", rb, r.RunID, r.ID)
		}
		if e != nil {
			return e
		}
	}
	for _, event := range v.Events {
		eb, _ := json.Marshal(event)
		if _, e = tx.Exec("INSERT OR IGNORE INTO events VALUES(?,?,?)", v.RunID, event.ID, eb); e != nil {
			return e
		}
	}
	if len(v.Report) > 0 {
		var report map[string]any
		if err := json.Unmarshal(v.Report, &report); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT request,result FROM commands WHERE run_id=? ORDER BY rowid", v.RunID)
		if err != nil {
			return err
		}
		journal := []map[string]json.RawMessage{}
		for rows.Next() {
			var request, result []byte
			if err = rows.Scan(&request, &result); err != nil {
				rows.Close()
				return err
			}
			journal = append(journal, map[string]json.RawMessage{"command": request, "result": result})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		report["commands"] = journal
		v.Report, err = json.Marshal(report)
		if err != nil {
			return err
		}
		b, err = json.Marshal(v)
		if err != nil {
			return err
		}
		if _, e = tx.Exec("INSERT OR REPLACE INTO snapshot VALUES(1,?)", b); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT OR REPLACE INTO runs VALUES(?,?)", v.RunID, []byte(v.Report)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Archive(run string, report json.RawMessage) error {
	_, e := s.DB.Exec("INSERT OR REPLACE INTO runs VALUES(?,?)", run, []byte(report))
	return e
}
func (s *Store) Runs() ([]json.RawMessage, error) {
	rows, e := s.DB.Query("SELECT body FROM runs ORDER BY rowid DESC LIMIT 50")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
