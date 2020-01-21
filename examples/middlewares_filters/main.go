package main

import (
	"github.com/savsgio/atreugo/v10"
	"github.com/savsgio/atreugo/v10/middlewares"
)

func main() {
	config := &atreugo.Config{
		Addr: "0.0.0.0:8000",
	}
	server := atreugo.New(config)

	// Register before middlewares
	server.UseBefore(middlewares.RequestIDMiddleware, beforeMiddleware)

	// Register after middlewares
	server.UseAfter(afterMiddleware)

	// Register a route with filters
	filters := atreugo.Filters{
		Before: []atreugo.Middleware{beforeFilter},
		After:  []atreugo.Middleware{afterFilter},
	}

	server.PathWithFilters("GET", "/", func(ctx *atreugo.RequestCtx) error {
		return ctx.TextResponse("Middlewares and view filters")
	}, filters)

	// Run
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}
