package apiserver

import (
	"testing"

	"github.com/ericsperano/yfh/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestImportFantasyGame(t *testing.T) {
	called := false
	fn := func(ctx *gin.Context, yfh *core.YFH, task *core.Task) {
		assert.Equal(t, core.TaskImportFantasyGame, task.Type)
		assert.Equal(t, map[string]string{core.TaskDataUser: DefaultUser}, task.Data)
		called = true
	}
	HandleImportFantasyGame(nil, fn)(nil)
	assert.True(t, called)
}
