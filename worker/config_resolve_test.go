package worker

import (
	"testing"

	"github.com/spf13/viper"
)

// capturingLogger records Warn calls for test assertions.
type capturingLogger struct {
	warnMessages []string
}

func (l *capturingLogger) Debug(string, ...interface{}) {}
func (l *capturingLogger) Info(string, ...interface{})  {}
func (l *capturingLogger) Warn(msg string, _ ...interface{}) {
	l.warnMessages = append(l.warnMessages, msg)
}
func (l *capturingLogger) Error(string, ...interface{}) {}

func intPtr(v int) *int { return &v }

func TestResolveConfigInt(t *testing.T) {
	tests := []struct {
		name        string
		param       configIntParam
		override    *int
		viperSetup  map[string]int
		want        int
		wantWarning bool
	}{
		{
			name:  "default only",
			param: configIntParam{Default: 5},
			want:  5,
		},
		{
			name:  "viper flag overrides default",
			param: configIntParam{Flag: "test-flag", Default: 5},
			viperSetup: map[string]int{
				"test-flag": 8,
			},
			want: 8,
		},
		{
			name:  "viper flag zero falls back to default",
			param: configIntParam{Flag: "test-flag", Default: 5},
			viperSetup: map[string]int{
				"test-flag": 0,
			},
			want: 5,
		},
		{
			name:     "override replaces default",
			param:    configIntParam{Default: 5},
			override: intPtr(7),
			want:     7,
		},
		{
			name:     "override replaces viper value",
			param:    configIntParam{Flag: "test-flag", Default: 5},
			override: intPtr(12),
			viperSetup: map[string]int{
				"test-flag": 8,
			},
			want: 12,
		},
		{
			name:  "nil override keeps resolved value",
			param: configIntParam{Default: 5},
			want:  5,
		},
		{
			name:     "zero override keeps resolved value",
			param:    configIntParam{Default: 5},
			override: intPtr(0),
			want:     5,
		},
		{
			name: "capping with MaxDefault",
			param: configIntParam{
				Default:    15,
				MaxDefault: 10,
			},
			want:        10,
			wantWarning: true,
		},
		{
			name: "capping with MaxFlag from viper",
			param: configIntParam{
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
			param: configIntParam{
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
			param: configIntParam{
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
			param: configIntParam{
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
			param: configIntParam{
				Default:    5,
				MaxDefault: 10,
			},
			want: 5,
		},
		{
			name: "value equal to max is not capped",
			param: configIntParam{
				Default:    10,
				MaxDefault: 10,
			},
			want: 10,
		},
		{
			name: "override capped at max",
			param: configIntParam{
				Default:    5,
				MaxDefault: 10,
			},
			override:    intPtr(20),
			want:        10,
			wantWarning: true,
		},
		{
			name: "season concurrency param defaults",
			param: configIntParam{
				Default:    5,
				MaxFlag:    "test-max-season",
				MaxDefault: 10,
			},
			want: 5,
		},
		{
			name: "season concurrency with override under max",
			param: configIntParam{
				Default:    5,
				MaxFlag:    "test-max-season",
				MaxDefault: 10,
			},
			override: intPtr(8),
			want:     8,
		},
		{
			name: "season concurrency with override over max",
			param: configIntParam{
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
			got := resolveConfigInt(logger, tt.param, tt.override)
			if got != tt.want {
				t.Errorf("resolveConfigInt() = %d, want %d", got, tt.want)
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
	got := resolveConfigInt(nil, configIntParam{Default: 15, MaxDefault: 10}, nil)
	if got != 10 {
		t.Errorf("resolveConfigInt() = %d, want 10", got)
	}
}
