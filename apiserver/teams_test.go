package apiserver

import (
	"testing"

	"github.com/ericsperano/yfh/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

/* TODO ctx cant be nil
func TestImportTeam(t *testing.T) {
	called := false
	fn := func(ctx *gin.Context, yfh *core.YFH, task *core.Task) {
		assert.Equal(t, core.TaskImportLeague, task.Type)
		assert.Equal(t, map[string]string{core.TaskDataUser: DefaultUser}, task.Data)
		called = true
	}
	HandleImportTeam(nil, fn)(nil)
	assert.True(t, called)
}
*/

func TestImportTeams(t *testing.T) {
	called := false
	fn := func(ctx *gin.Context, yfh *core.YFH, task *core.Task) {
		assert.Equal(t, core.TaskImportTeams, task.Type)
		assert.Equal(t, map[string]string{core.TaskDataUser: DefaultUser}, task.Data)
		called = true
	}
	HandleImportTeams(nil, fn)(nil)
	assert.True(t, called)
}
