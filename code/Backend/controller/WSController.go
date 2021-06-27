package controller

import (
	"backend/lib/wsWrap"
	"backend/service"
	"backend/utils"
	"github.com/kataras/iris/v12/context"
)

var wss *wsWrap.WSServer

func init() {
	wss = wsWrap.NewWSServer(onConn, onDisConn, onMessage)
}

func onConn(id string) {
	ns, uid, username, fid := utils.ParseID(id)
	switch ns {
	case "sheet":
		service.SheetOnConn(uid, username, fid)
	}
}

func onDisConn(id string) {
	ns, uid, username, fid := utils.ParseID(id)
	switch ns {
	case "sheet":
		service.SheetOnDisConn(uid, username, fid)
	}
}

func onMessage(id string, body []byte) {
	ns, uid, username, fid := utils.ParseID(id)
	switch ns {
	case "sheet":
		service.SheetOnMessage(wss, uid, username, fid, body)
	}
}

func SheetUpgradeHandler() context.Handler {
	idGen := func(ctx *context.Context) string {
		ns := ctx.Params().Get("ns")
		uid, _ := ctx.Params().GetUint("uid")
		username := ctx.Params().Get("username")
		fid, _ := ctx.Params().GetUint("fid")
		return utils.GenID(ns, uid, username, fid)
	}
	return wss.Handler(idGen)
}

func SheetBeforeUpgradeHandler() context.Handler {
	return func(ctx *context.Context) {
		fid := uint(ctx.URLParamUint64("fid"))
		token := ctx.URLParam("token")
		if success, msg, user := service.SheetOnConnEstablished(token, fid); !success {
			utils.SendResponse(ctx, success, msg, nil)
			ctx.StopExecution()
		} else {
			ctx.Params().Save("ns", "sheet", false)
			ctx.Params().Save("uid", user.Uid, false)
			ctx.Params().Save("username", user.Username, false)
			ctx.Params().Save("fid", fid, false)
			ctx.Next()
		}
	}
}