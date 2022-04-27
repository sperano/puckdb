package apiserver

import (
	"testing"

	"github.com/ericsperano/yfh/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestImportLeague(t *testing.T) {
	called := false
	fn := func(ctx *gin.Context, yfh *core.YFH, task *core.Task) {
		assert.Equal(t, core.TaskImportLeague, task.Type)
		assert.Equal(t, map[string]string{core.TaskDataUser: DefaultUser}, task.Data)
		called = true
	}
	HandleImportLeague(nil, fn)(nil)
	assert.True(t, called)
}
