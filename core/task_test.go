package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskString(t *testing.T) {
	task := Task{
		Type: "mah-task",
		Data: map[string]string{
			"foo": "bar",
			"lol": "mdr",
		},
	}
	exp := `mah-task {foo: "bar", lol: "mdr"}`
	assert.Equal(t, exp, task.String())
}
