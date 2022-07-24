package core

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLocalFileInfoFilename(t *testing.T) {
	t.Parallel()
	now, _ := ParseTimestamp("20210930024225")
	f := LocalFile{
		Name:      "foo",
		Extension: "xml",
		Time:      now,
	}
	exp := fmt.Sprintf("foo_%4d%02d%02d%02d%02d%02d.xml", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second())
	assert.Equal(t, exp, f.Filename())
}

func TestLocalParseFilenameSuccess(t *testing.T) {
	t.Parallel()
	file := ParseLocalFilename("foo_20210930024225.xml")
	assert.NotNil(t, file)
	assert.Equal(t, "foo", file.Name)
	assert.Equal(t, "xml", file.Extension)
	assert.Equal(t, 2021, file.Time.Year())
	assert.Equal(t, time.September, file.Time.Month())
	assert.Equal(t, 30, file.Time.Day())
	assert.Equal(t, 2, file.Time.Hour())
	assert.Equal(t, 42, file.Time.Minute())
	assert.Equal(t, 25, file.Time.Second())
}

func TestLocalParseFilenameIgnores(t *testing.T) {
	t.Parallel()
	// no underscores
	assert.Nil(t, ParseLocalFilename("foo20210930024225.xml"))
	// more than one underscore
	assert.Nil(t, ParseLocalFilename("foo_foo_20210930024225.xml"))
	// no dots
	assert.Nil(t, ParseLocalFilename("foo_20210930024225xml"))
	// more than one dot
	assert.Nil(t, ParseLocalFilename("foo_20210930024225.xml.bkp"))
	// invalid timestamp
	assert.Nil(t, ParseLocalFilename("foo_2021093lol0024225.xml"))
}
