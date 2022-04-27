package apiserver

import (
	"net/http"

	"github.com/ericsperano/yfh/core"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
)

func HandleYahooLogin(ctx *gin.Context) {
	if conf, err := core.GetOauthConfig(); err == nil {
		url := conf.AuthCodeURL("state", oauth2.AccessTypeOnline)
		ctx.Redirect(http.StatusMovedPermanently, url)
	} else {
		HandleError(ctx, http.StatusForbidden, err)
	}
}
