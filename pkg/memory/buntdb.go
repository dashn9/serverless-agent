package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tidwall/buntdb"
)

type FunctionConfig struct {
	Name                   string
	Handler                string
	Resources              ResourceLimits
	Timeout                int32
	MaxConcurrency         int32
	MaxConcurrencyBehavior string
	Env                    map[string]string
}

type ResourceLimits struct {
	CPU    int32
	Memory int64
}

type Store struct {
	db *buntdb.DB
}

func NewStore() (*Store, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	path := filepath.Join(filepath.Dir(exe), "agent.db")
	db, err := buntdb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open agent store at %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) SaveFunction(config *FunctionConfig) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *buntdb.Tx) error {
		_, _, err := tx.Set(fmt.Sprintf("agent:function:%s", config.Name), string(data), nil)
		return err
	})
}

func (s *Store) GetFunction(name string) (*FunctionConfig, error) {
	var val string
	err := s.db.View(func(tx *buntdb.Tx) error {
		var e error
		val, e = tx.Get(fmt.Sprintf("agent:function:%s", name))
		return e
	})
	if err == buntdb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg FunctionConfig
	if err := json.Unmarshal([]byte(val), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (s *Store) GetAllFunctions() ([]*FunctionConfig, error) {
	var functions []*FunctionConfig
	err := s.db.View(func(tx *buntdb.Tx) error {
		return tx.AscendKeys("agent:function:*", func(key, val string) bool {
			var cfg FunctionConfig
			if err := json.Unmarshal([]byte(val), &cfg); err == nil {
				functions = append(functions, &cfg)
			}
			return true
		})
	})
	return functions, err
}

func (s *Store) SaveExecution(executionID, agentID, functionName, status, errMsg string, output []byte, durationMs int64, startedAt time.Time, statusAt *time.Time) error {
	type record struct {
		ExecutionID  string     `json:"execution_id"`
		AgentID      string     `json:"agent_id"`
		FunctionName string     `json:"function_name"`
		Status       string     `json:"status"`
		Output       string     `json:"output,omitempty"`
		Error        string     `json:"error,omitempty"`
		DurationMs   int64      `json:"duration_ms,omitempty"`
		StartedAt    time.Time  `json:"started_at"`
		StatusAt     *time.Time `json:"status_at,omitempty"`
	}
	data, err := json.Marshal(record{
		ExecutionID:  executionID,
		AgentID:      agentID,
		FunctionName: functionName,
		Status:       status,
		Output:       string(output),
		Error:        errMsg,
		DurationMs:   durationMs,
		StartedAt:    startedAt,
		StatusAt:     statusAt,
	})
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *buntdb.Tx) error {
		_, _, err := tx.Set(
			fmt.Sprintf("flux:exec:%s", executionID),
			string(data),
			&buntdb.SetOptions{Expires: true, TTL: time.Hour},
		)
		return err
	})
}

func (s *Store) GetExecution(executionID string) ([]byte, error) {
	var val string
	err := s.db.View(func(tx *buntdb.Tx) error {
		var e error
		val, e = tx.Get(fmt.Sprintf("flux:exec:%s", executionID))
		return e
	})
	if err == buntdb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(val), nil
}
