package atreugo

import (
	"net/http"
	"sort"
	"strings"

	fastrouter "github.com/fasthttp/router"
	gstrings "github.com/savsgio/gotils/strings"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

func defaultErrorView(ctx *RequestCtx, err error, statusCode int) {
	ctx.Error(err.Error(), statusCode)
}

func emptyView(ctx *RequestCtx) error {
	return nil
}

func buildOptionsView(url string, fn View, paths map[string][]string) View {
	allow := make([]string, 0)

	for method, urls := range paths {
		if method == fasthttp.MethodOptions || !gstrings.Include(urls, url) {
			continue
		}

		allow = append(allow, method)
	}

	if len(allow) == 0 {
		allow = append(allow, fasthttp.MethodOptions)
	}

	sort.Strings(allow)
	allowValue := strings.Join(allow, ", ")

	return func(ctx *RequestCtx) error {
		ctx.Response.Header.Set(fasthttp.HeaderAllow, allowValue)

		return fn(ctx)
	}
}

func newRouter(cfg Config) *Router {
	router := fastrouter.New()
	router.HandleOPTIONS = false

	return &Router{
		router:        router,
		handleOPTIONS: true,
		cfg: &routerConfig{
			errorView: cfg.ErrorView,
			debug:     cfg.Debug,
			logger:    cfg.Logger,
		},
	}
}

func (r *Router) mutable(v bool) {
	if v != r.routerMutable {
		r.routerMutable = v
		r.router.Mutable(v)
	}
}

func (r *Router) buildMiddlewares(m Middlewares) Middlewares {
	m2 := Middlewares{}
	m2.Before = append(m2.Before, r.middlewares.Before...)
	m2.Before = append(m2.Before, m.Before...)
	m2.After = append(m2.After, m.After...)
	m2.After = append(m2.After, r.middlewares.After...)

	m2.Skip = append(m2.Skip, m.Skip...)
	m2.Skip = append(m2.Skip, r.middlewares.Skip...)

	switch {
	case r.parent != nil:
		return r.parent.buildMiddlewares(m2)
	case r.cfg.debug:
		debugMiddleware := func(ctx *RequestCtx) error {
			r.cfg.logger.Printf("%s %s", ctx.Method(), ctx.URI())

			return ctx.Next()
		}

		m2.Before = append([]Middleware{debugMiddleware}, m2.Before...)
	}

	m2.Before = appendMiddlewares(m2.Before[:0], m2.Before, m2.Skip...)
	m2.After = appendMiddlewares(m2.After[:0], m2.After, m2.Skip...)

	return m2
}

func (r *Router) getGroupFullPath(path string) string {
	if r.parent != nil {
		path = r.parent.getGroupFullPath(r.prefix + path)
	}

	return path
}

func (r *Router) handler(fn View, middle Middlewares) fasthttp.RequestHandler {
	middle = r.buildMiddlewares(middle)

	chain := append(middle.Before, func(ctx *RequestCtx) error {
		if !ctx.skipView {
			if err := fn(ctx); err != nil {
				return err
			}
		}

		return ctx.Next()
	})
	chain = append(chain, middle.After...)
	chainLen := len(chain)

	return func(ctx *fasthttp.RequestCtx) {
		actx := AcquireRequestCtx(ctx)

		for i := 0; i < chainLen; i++ {
			if err := chain[i](actx); err != nil {
				statusCode := actx.Response.StatusCode()
				if statusCode == fasthttp.StatusOK {
					statusCode = fasthttp.StatusInternalServerError
				}

				r.cfg.logger.Printf("error view: %s", err)
				r.cfg.errorView(actx, err, statusCode)

				break
			} else if !actx.next {
				break
			}

			actx.next = false
		}

		ReleaseRequestCtx(actx)
	}
}

func (r *Router) handlePath(p *Path) {
	isOPTIONS := p.method == fasthttp.MethodOptions

	switch {
	case p.registered:
		r.mutable(true)
	case isOPTIONS:
		mutable := !gstrings.Include(r.customOPTIONS, p.url)
		r.mutable(mutable)
	case r.routerMutable:
		r.mutable(false)
	}

	view := p.view
	if isOPTIONS {
		view = buildOptionsView(p.url, view, r.ListPaths())
		r.customOPTIONS = gstrings.UniqueAppend(r.customOPTIONS, p.url)
	}

	handler := r.handler(view, p.middlewares)
	if p.withTimeout {
		handler = fasthttp.TimeoutWithCodeHandler(handler, p.timeout, p.timeoutMsg, p.timeoutCode)
	}

	r.router.Handle(p.method, p.url, handler)

	if r.handleOPTIONS && !p.registered && !isOPTIONS {
		view = buildOptionsView(p.url, emptyView, r.ListPaths())
		handler = r.handler(view, p.middlewares)

		r.mutable(true)
		r.router.Handle(fasthttp.MethodOptions, p.url, handler)
	}
}

// NewGroupPath returns a new router to group paths.
func (r *Router) NewGroupPath(path string) *Router {
	return &Router{
		parent:        r,
		prefix:        path,
		router:        r.router,
		routerMutable: r.routerMutable,
		handleOPTIONS: r.handleOPTIONS,
		cfg:           r.cfg,
	}
}

// ListPaths returns all registered routes grouped by method.
func (r *Router) ListPaths() map[string][]string {
	return r.router.List()
}

// Middlewares defines the middlewares (before, after and skip) in the order in which you want to execute them
// for the view or group
//
// WARNING: The previous middlewares configuration could be overridden.
func (r *Router) Middlewares(middlewares Middlewares) *Router {
	r.middlewares = middlewares

	return r
}

// UseBefore registers the middlewares in the order in which you want to execute them
// before the execution of the view or group.
func (r *Router) UseBefore(fns ...Middleware) *Router {
	r.middlewares.Before = append(r.middlewares.Before, fns...)

	return r
}

// UseAfter registers the middlewares in the order in which you want to execute them
// after the execution of the view or group.
func (r *Router) UseAfter(fns ...Middleware) *Router {
	r.middlewares.After = append(r.middlewares.After, fns...)

	return r
}

// SkipMiddlewares registers the middlewares that you want to skip when executing the view or group.
func (r *Router) SkipMiddlewares(fns ...Middleware) *Router {
	r.middlewares.Skip = append(r.middlewares.Skip, fns...)

	return r
}

// GET shortcut for router.Path("GET", url, viewFn).
func (r *Router) GET(url string, viewFn View) *Path {
	return r.Path(fasthttp.MethodGet, url, viewFn)
}

// HEAD shortcut for router.Path("HEAD", url, viewFn).
func (r *Router) HEAD(url string, viewFn View) *Path {
	return r.Path(fasthttp.MethodHead, url, viewFn)
}

// OPTIONS shortcut for router.Path("OPTIONS", url, viewFn).
func (r *Router) OPTIONS(url string, viewFn View) *Path {
	return r.Path(fasthttp.MethodOptions, url, viewFn)
}

// POST shortcut for router.Path("POST", url, viewFn).
func (r *Router) POST(url string, viewFn View) *Path {
	return r.Path(fasthttp.MethodPost, url, viewFn)
}

// PUT shortcut for router.Path("PUT", url, viewFn).
func (r *Router) PUT(url string, viewFn View) *Path {
	return r.Path(fasthttp.MethodPut, url, viewFn)
}

// PATCH shortcut for router.Path("PATCH", url, viewFn).
func (r *Router) PATCH(url string, viewFn View) *Path {
	return r.Path(fasthttp.MethodPatch, url, viewFn)
}

// DELETE shortcut for router.Path("DELETE", url, viewFn).
func (r *Router) DELETE(url string, viewFn View) *Path {
	return r.Path(fasthttp.MethodDelete, url, viewFn)
}

// ANY shortcut for router.Path("*", url, viewFn)
//
// WARNING: Use only for routes where the request method is not important.
func (r *Router) ANY(url string, viewFn View) *Path {
	return r.Path(fastrouter.MethodWild, url, viewFn)
}

// RequestHandlerPath wraps fasthttp request handler to atreugo view and registers it to
// the given path and method.
func (r *Router) RequestHandlerPath(method, url string, handler fasthttp.RequestHandler) *Path {
	viewFn := func(ctx *RequestCtx) error {
		handler(ctx.RequestCtx)

		return nil
	}

	return r.Path(method, url, viewFn)
}

// NetHTTPPath wraps net/http handler to atreugo view and registers it to
// the given path and method.
//
// While this function may be used for easy switching from net/http to fasthttp/atreugo,
// it has the following drawbacks comparing to using manually written fasthttp/atreugo,
// request handler:
//
//     * A lot of useful functionality provided by fasthttp/atreugo is missing
//       from net/http handler.
//     * net/http -> fasthttp/atreugo handler conversion has some overhead,
//       so the returned handler will be always slower than manually written
//       fasthttp/atreugo handler.
//
// So it is advisable using this function only for quick net/http -> fasthttp
// switching. Then manually convert net/http handlers to fasthttp handlers.
// according to https://github.com/valyala/fasthttp#switching-from-nethttp-to-fasthttp.
func (r *Router) NetHTTPPath(method, url string, handler http.Handler) *Path {
	h := fasthttpadaptor.NewFastHTTPHandler(handler)

	return r.RequestHandlerPath(method, url, h)
}

// Static serves static files from the given file system root
//
// Make sure your program has enough 'max open files' limit aka
// 'ulimit -n' if root folder contains many files.
func (r *Router) Static(url, rootPath string) *Path {
	return r.StaticCustom(url, &StaticFS{
		Root:               rootPath,
		IndexNames:         []string{"index.html"},
		GenerateIndexPages: true,
		AcceptByteRange:    true,
	})
}

// StaticCustom serves static files from the given file system settings
//
// Make sure your program has enough 'max open files' limit aka
// 'ulimit -n' if root folder contains many files.
func (r *Router) StaticCustom(url string, fs *StaticFS) *Path {
	if strings.HasSuffix(url, "/") {
		url = url[:len(url)-1]
	}

	ffs := &fasthttp.FS{
		Root:                 fs.Root,
		IndexNames:           fs.IndexNames,
		GenerateIndexPages:   fs.GenerateIndexPages,
		Compress:             fs.Compress,
		AcceptByteRange:      fs.AcceptByteRange,
		CacheDuration:        fs.CacheDuration,
		CompressedFileSuffix: fs.CompressedFileSuffix,
	}

	if fs.PathNotFound != nil {
		ffs.PathNotFound = viewToHandler(fs.PathNotFound, r.cfg.errorView)
	}

	if fs.PathRewrite != nil {
		ffs.PathRewrite = func(ctx *fasthttp.RequestCtx) []byte {
			actx := AcquireRequestCtx(ctx)
			result := fs.PathRewrite(actx)
			ReleaseRequestCtx(actx)

			return result
		}
	}

	stripSlashes := strings.Count(r.getGroupFullPath(url), "/")

	if ffs.PathRewrite == nil && stripSlashes > 0 {
		ffs.PathRewrite = fasthttp.NewPathSlashesStripper(stripSlashes)
	}

	return r.RequestHandlerPath(fasthttp.MethodGet, url+"/{filepath:*}", ffs.NewRequestHandler())
}

// ServeFile returns HTTP response containing compressed file contents
// from the given path
//
// HTTP response may contain uncompressed file contents in the following cases:
//
//   * Missing 'Accept-Encoding: gzip' request header.
//   * No write access to directory containing the file.
//
// Directory contents is returned if path points to directory.
func (r *Router) ServeFile(url, filePath string) *Path {
	viewFn := func(ctx *RequestCtx) error {
		fasthttp.ServeFile(ctx.RequestCtx, filePath)

		return nil
	}

	return r.Path(fasthttp.MethodGet, url, viewFn)
}

// Path registers a new view with the given path and method
//
// This function is intended for bulk loading and to allow the usage of less
// frequently used, non-standardized or custom methods (e.g. for internal
// communication with a proxy).
func (r *Router) Path(method, url string, viewFn View) *Path {
	if method != strings.ToUpper(method) {
		panic("The http method '" + method + "' must be in uppercase")
	}

	p := &Path{
		router: r,
		method: method,
		url:    r.getGroupFullPath(url),
		view:   viewFn,
	}

	r.handlePath(p)

	p.registered = true

	return p
}
