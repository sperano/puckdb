package shared

import (
	"testing"

	"github.com/spf13/viper"
)

// capturingLogger records Warn calls for test assertions.
type capturingLogger struct {
	warnMessages []string
}

func (l *capturingLogger) Debug(string, ...any) {}
func (l *capturingLogger) Info(string, ...any)  {}
func (l *capturingLogger) Warn(msg string, _ ...any) {
	l.warnMessages = append(l.warnMessages, msg)
}
func (l *capturingLogger) Error(string, ...any) {}

func intPtr(v int) *int { return &v }

func TestResolveConfigInt(t *testing.T) {
	tests := []struct {
		name        string
		param       ConfigIntParam
		override    *int
		viperSetup  map[string]int
		want        int
		wantWarning bool
	}{
		{
			name:  "default only",
			param: ConfigIntParam{Default: 5},
			want:  5,
		},
		{
			name:  "viper flag overrides default",
			param: ConfigIntParam{Flag: "test-flag", Default: 5},
			viperSetup: map[string]int{
				"test-flag": 8,
			},
			want: 8,
		},
		{
			name:  "viper flag zero falls back to default",
			param: ConfigIntParam{Flag: "test-flag", Default: 5},
			viperSetup: map[string]int{
				"test-flag": 0,
			},
			want: 5,
		},
		{
			name:     "override replaces default",
			param:    ConfigIntParam{Default: 5},
			override: intPtr(7),
			want:     7,
		},
		{
			name:     "override replaces viper value",
			param:    ConfigIntParam{Flag: "test-flag", Default: 5},
			override: intPtr(12),
			viperSetup: map[string]int{
				"test-flag": 8,
			},
			want: 12,
		},
		{
			name:  "nil override keeps resolved value",
			param: ConfigIntParam{Default: 5},
			want:  5,
		},
		{
			name:     "zero override keeps resolved value",
			param:    ConfigIntParam{Default: 5},
			override: intPtr(0),
			want:     5,
		},
		{
			name: "capping with MaxDefault",
			param: ConfigIntParam{
				Default:    15,
				MaxDefault: 10,
			},
			want:        10,
			wantWarning: true,
		},
		{
			name: "capping with MaxFlag from viper",
			param: ConfigIntParam{
				Default: 15,
				MaxFlag: "test-max",
			},
			viperSetup: map[string]int{
				"test-max": 10,
			},
			want:        10,
			wantWarning: true,
		},
		{
			name: "MaxFlag overrides MaxDefault",
			param: ConfigIntParam{
				Default:    25,
				MaxFlag:    "test-max",
				MaxDefault: 20,
			},
			viperSetup: map[string]int{
				"test-max": 15,
			},
			want:        15,
			wantWarning: true,
		},
		{
			name: "MaxFlag zero falls back to MaxDefault",
			param: ConfigIntParam{
				Default:    15,
				MaxFlag:    "test-max",
				MaxDefault: 10,
			},
			viperSetup: map[string]int{
				"test-max": 0,
			},
			want:        10,
			wantWarning: true,
		},
		{
			name: "MaxDefault zero means no cap when MaxFlag absent",
			param: ConfigIntParam{
				Default:    100,
				MaxFlag:    "test-max",
				MaxDefault: 0,
			},
			viperSetup: map[string]int{
				"test-max": 0,
			},
			want: 100,
		},
		{
			name: "value under max is not capped",
			param: ConfigIntParam{
				Default:    5,
				MaxDefault: 10,
			},
			want: 5,
		},
		{
			name: "value equal to max is not capped",
			param: ConfigIntParam{
				Default:    10,
				MaxDefault: 10,
			},
			want: 10,
		},
		{
			name: "override capped at max",
			param: ConfigIntParam{
				Default:    5,
				MaxDefault: 10,
			},
			override:    intPtr(20),
			want:        10,
			wantWarning: true,
		},
		{
			name: "season concurrency param defaults",
			param: ConfigIntParam{
				Default:    5,
				MaxFlag:    "test-max-season",
				MaxDefault: 10,
			},
			want: 5,
		},
		{
			name: "season concurrency with override under max",
			param: ConfigIntParam{
				Default:    5,
				MaxFlag:    "test-max-season",
				MaxDefault: 10,
			},
			override: intPtr(8),
			want:     8,
		},
		{
			name: "season concurrency with override over max",
			param: ConfigIntParam{
				Default:    5,
				MaxFlag:    "test-max-season",
				MaxDefault: 10,
			},
			override:    intPtr(15),
			want:        10,
			wantWarning: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			for k, v := range tt.viperSetup {
				viper.Set(k, v)
			}

			logger := &capturingLogger{}
			got := ResolveConfigInt(logger, tt.param, tt.override)
			if got != tt.want {
				t.Errorf("ResolveConfigInt() = %d, want %d", got, tt.want)
			}
			if tt.wantWarning && len(logger.warnMessages) == 0 {
				t.Error("expected warning log, got none")
			}
			if !tt.wantWarning && len(logger.warnMessages) > 0 {
				t.Errorf("unexpected warning: %v", logger.warnMessages)
			}
		})
	}
}

func TestResolveConfigInt_NilLogger(t *testing.T) {
	viper.Reset()

	// Should not panic with nil logger even when capping
	got := ResolveConfigInt(nil, ConfigIntParam{Default: 15, MaxDefault: 10}, nil)
	if got != 10 {
		t.Errorf("ResolveConfigInt() = %d, want 10", got)
	}
}
