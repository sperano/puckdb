package database

import (
	"errors"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestPositionNameString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "BN", BN.String())
	assert.Equal(t, "IR", IR.String())
	assert.Equal(t, "IR+", IRPlus.String())
	assert.Equal(t, "C", C.String())
	assert.Equal(t, "LW", LW.String())
	assert.Equal(t, "RW", RW.String())
	assert.Equal(t, "D", D.String())
	assert.Equal(t, "Util", Util.String())
	assert.Equal(t, "G", G.String())
	assert.Equal(t, "C,LW,RW,D,Util,BN,IR", (C | LW | RW | D | Util | BN | IR).String())
}

func TestScanPositionName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
		want PositionName
		err  error
	}{
		{name: "C", data: "C", want: C, err: nil},
		{name: "LW", data: "LW", want: LW, err: nil},
		{name: "RW", data: "RW", want: RW, err: nil},
		{name: "D", data: "D", want: D, err: nil},
		{name: "G", data: "G", want: G, err: nil},
		{name: "BN", data: "BN", want: BN, err: nil},
		{name: "IR", data: "IR", want: IR, err: nil},
		{name: "IR+", data: "IR+", want: IRPlus, err: nil},
		{name: "Util", data: "Util", want: Util, err: nil},
		{name: "All", data: "C,LW,G,IR,RW,D,Util,BN", want: C | LW | RW | D | Util | G | BN | IR, err: nil},
		{name: "invalid", data: "invalid", want: -1, err: errors.New("can't parse RosterPosition: invalid")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt, err := ScanPositionName(tt.data)
			assert.Equal(t, tt.want, pt)
			assert.Equal(t, tt.err, err)
		})
	}
}
