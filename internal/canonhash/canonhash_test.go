package canonhash

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sha256("") from FIPS 180-2.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// sha256(`{"A":1,"B":"x"}`).
const objectSHA256 = "0f3656c3fd7ee6de73723072536d83aaeb349d3a6ac53ea704fcb07c7329f07e"

type object struct {
	A int
	B string
}

func TestBytes(t *testing.T) {
	assert.Equal(t, emptySHA256, Bytes(nil))
	assert.Equal(t, emptySHA256, Bytes([]byte{}))
}

func TestJSONHashesTheEncoding(t *testing.T) {
	hash, err := JSON(object{A: 1, B: "x"})
	require.NoError(t, err)
	assert.Equal(t, objectSHA256, hash)
	assert.Equal(t, Bytes([]byte(`{"A":1,"B":"x"}`)), hash)
}

func TestJSONMapKeysAreSorted(t *testing.T) {
	first, err := JSON(map[string]int{"b": 2, "a": 1})
	require.NoError(t, err)
	assert.Equal(t, Bytes([]byte(`{"a":1,"b":2}`)), first)
}

func TestVersionedPrefixesTheDigest(t *testing.T) {
	hash, err := Versioned("v3", object{A: 1, B: "x"})
	require.NoError(t, err)
	assert.Equal(t, "v3:"+objectSHA256, hash)
}

func TestMarshalFailuresAreReturned(t *testing.T) {
	for name, value := range map[string]any{
		"NaN":      math.NaN(),
		"infinity": struct{ F float64 }{math.Inf(1)},
		"channel":  make(chan int),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := JSON(value)
			require.Error(t, err)
			_, err = Versioned("v1", value)
			require.Error(t, err)
		})
	}
}
