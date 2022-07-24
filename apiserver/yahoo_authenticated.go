package apiserver

import (
	"context"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/spf13/viper"

	"github.com/ericsperano/yfh/core"
	"github.com/gin-gonic/gin"
)

const FlagUIURL = "ui_url"

func HandleYahooAuthenticated(yfh *core.YFH) func(c *gin.Context) {
	return func(ctx *gin.Context) {
		code := ctx.Query("code")
		log.Debug().Str("code", code)
		ctxV := context.WithValue(ctx, core.CtxUser, DefaultUser)
		err := exchangeCode(ctxV, yfh, DefaultUser, code)
		if err == nil {
			ctx.Redirect(http.StatusMovedPermanently, viper.GetString(FlagUIURL))
		} else {
			HandleError(ctx, http.StatusForbidden, err)
		}
	}
}

func exchangeCode(ctx context.Context, yfh *core.YFH, user string, code string) error {
	conf, err := core.GetOauthConfig()
	if err != nil {
		return err
	}
	token, err := conf.Exchange(ctx, code)
	if err != nil {
		return err
	}
	return core.SaveToken(ctx, yfh, token)
}
