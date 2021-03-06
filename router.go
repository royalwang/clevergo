// Copyright 2013 Julien Schmidt. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package clevergo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type contextKey int

const (
	paramsKey contextKey = iota
	routeKey
)

// Param is a single URL parameter, consisting of a key and a value.
type Param struct {
	Key   string
	Value string
}

// Params is a Param-slice, as returned by the router.
// The slice is ordered, the first URL parameter is also the first slice value.
// It is therefore safe to read values by the index.
type Params []Param

// Get returns the value of the first Param which key matches the given name.
// If no matching Param is found, an empty string is returned.
func (ps Params) Get(name string) string {
	for _, p := range ps {
		if p.Key == name {
			return p.Value
		}
	}
	return ""
}

// Bool returns the boolean value of the given name.
func (ps Params) Bool(name string) (bool, error) {
	return strconv.ParseBool(ps.Get(name))
}

// Float64 returns the float64 value of the given name.
func (ps Params) Float64(name string) (float64, error) {
	return strconv.ParseFloat(ps.Get(name), 64)
}

// Int returns the int value of the given name.
func (ps Params) Int(name string) (int, error) {
	return strconv.Atoi(ps.Get(name))
}

// Int64 returns the int64 value of the given name.
func (ps Params) Int64(name string) (int64, error) {
	return strconv.ParseInt(ps.Get(name), 10, 64)
}

// Uint64 returns the uint64 value of the given name.
func (ps Params) Uint64(name string) (uint64, error) {
	return strconv.ParseUint(ps.Get(name), 10, 64)
}

// GetParams returns params of the request.
func GetParams(req *http.Request) Params {
	ps, _ := req.Context().Value(paramsKey).(Params)
	return ps
}

// GetRoute returns matched route of the request, it
// only works if Router.SaveMatchedRoute is turn on.
func GetRoute(req *http.Request) *Route {
	r, _ := req.Context().Value(routeKey).(*Route)
	return r
}

// Router is a http.Handler which can be used to dispatch requests to different
// handler functions via configurable routes
type Router struct {
	trees map[string]*node

	// Named routes.
	routes map[string]*Route

	paramsPool sync.Pool
	maxParams  uint16

	// If enabled, adds the matched route onto the http.Request context
	// before invoking the handler.
	SaveMatchedRoute bool

	// Enables automatic redirection if the current route can't be matched but a
	// handler for the path with (without) the trailing slash exists.
	// For example if /foo/ is requested but a route only exists for /foo, the
	// client is redirected to /foo with http status code 301 for Get requests
	// and 308 for all other request methods.
	RedirectTrailingSlash bool

	// If enabled, the router tries to fix the current request path, if no
	// handle is registered for it.
	// First superfluous path elements like ../ or // are removed.
	// Afterwards the router does a case-insensitive lookup of the cleaned path.
	// If a handle can be found for this route, the router makes a redirection
	// to the corrected path with status code 301 for Get requests and 308 for
	// all other request methods.
	// For example /FOO and /..//Foo could be redirected to /foo.
	// RedirectTrailingSlash is independent of this option.
	RedirectFixedPath bool

	// If enabled, the router checks if another method is allowed for the
	// current route, if the current request can not be routed.
	// If this is the case, the request is answered with 'Method Not Allowed'
	// and HTTP status code 405.
	// If no other Method is allowed, the request is delegated to the NotFound
	// handler.
	HandleMethodNotAllowed bool

	// If enabled, the router automatically replies to OPTIONS requests.
	// Custom OPTIONS handlers take priority over automatic replies.
	HandleOPTIONS bool

	// An optional http.Handler that is called on automatic OPTIONS requests.
	// The handler is only called if HandleOPTIONS is true and no OPTIONS
	// handler for the specific path was set.
	// The "Allowed" header is set before calling the handler.
	GlobalOPTIONS http.Handler

	// Cached value of global (*) allowed methods
	globalAllowed string

	// Configurable http.Handler which is called when no matching route is
	// found. If it is not set, http.NotFound is used.
	NotFound http.Handler

	// Configurable http.Handler which is called when a request
	// cannot be routed and HandleMethodNotAllowed is true.
	// If it is not set, http.Error with http.StatusMethodNotAllowed is used.
	// The "Allow" header with allowed request methods is set before the handler
	// is called.
	MethodNotAllowed http.Handler
}

// Make sure the Router conforms with the http.Handler interface
var _ http.Handler = NewRouter()

// NewRouter returns a new initialized Router.
// Path auto-correction, including trailing slashes, is enabled by default.
func NewRouter() *Router {
	return &Router{
		RedirectTrailingSlash:  true,
		RedirectFixedPath:      true,
		HandleMethodNotAllowed: true,
		HandleOPTIONS:          true,
	}
}

func (r *Router) getParams() *Params {
	ps := r.paramsPool.Get().(*Params)
	*ps = (*ps)[0:0] // reset slice
	return ps
}

func (r *Router) putParams(ps *Params) {
	r.paramsPool.Put(ps)
}

// URL creates an url with the given route name and arguments.
func (r *Router) URL(name string, args ...string) (*url.URL, error) {
	if route, ok := r.routes[name]; ok {
		return route.URL(args...)
	}

	return nil, fmt.Errorf("route %q does not exist", name)
}

// Group creates route group with the given path and optional route options.
func (r *Router) Group(path string, opts ...RouteGroupOption) *RouteGroup {
	return newRouteGroup(r, path, opts...)
}

// Get is a shortcut of Router.HandleFunc(http.MethodGet, path, handle, opts ...)
func (r *Router) Get(path string, handle http.HandlerFunc, opts ...RouteOption) {
	r.HandleFunc(http.MethodGet, path, handle, opts...)
}

// Head is a shortcut of Router.HandleFunc(http.MethodHead, path, handle, opts ...)
func (r *Router) Head(path string, handle http.HandlerFunc, opts ...RouteOption) {
	r.HandleFunc(http.MethodHead, path, handle)
}

// Options is a shortcut of Router.HandleFunc(http.MethodOptions, path, handle, opts ...)
func (r *Router) Options(path string, handle http.HandlerFunc, opts ...RouteOption) {
	r.HandleFunc(http.MethodOptions, path, handle)
}

// Post is a shortcut of Router.HandleFunc(http.MethodPost, path, handle, opts ...)
func (r *Router) Post(path string, handle http.HandlerFunc, opts ...RouteOption) {
	r.HandleFunc(http.MethodPost, path, handle)
}

// Put is a shortcut of Router.HandleFunc(http.MethodPut, path, handle, opts ...)
func (r *Router) Put(path string, handle http.HandlerFunc, opts ...RouteOption) {
	r.HandleFunc(http.MethodPut, path, handle)
}

// Patch is a shortcut of Router.HandleFunc(http.MethodPatch, path, handle, opts ...)
func (r *Router) Patch(path string, handle http.HandlerFunc, opts ...RouteOption) {
	r.HandleFunc(http.MethodPatch, path, handle)
}

// Delete is a shortcut of Router.HandleFunc(http.MethodDelete, path, handle, opts ...)
func (r *Router) Delete(path string, handle http.HandlerFunc, opts ...RouteOption) {
	r.HandleFunc(http.MethodDelete, path, handle)
}

// HandleFunc registers a new request handler function with the given path, method and optional route options.
//
// For Get, Head, Options, Post, Put, Patch and Delete requests the respective shortcut
// functions can be used.
//
// This function is intended for bulk loading and to allow the usage of less
// frequently used, non-standardized or custom methods (e.g. for internal
// communication with a proxy).
func (r *Router) HandleFunc(method, path string, handle http.HandlerFunc, opts ...RouteOption) {
	if handle == nil {
		panic("handle must not be nil")
	}
	r.Handle(method, path, http.HandlerFunc(handle), opts...)
}

// Handle registers a new request handler with the given path, method and optional route options.
func (r *Router) Handle(method, path string, handler http.Handler, opts ...RouteOption) {
	if method == "" {
		panic("method must not be empty")
	}
	if len(path) < 1 || path[0] != '/' {
		panic("path must begin with '/' in path '" + path + "'")
	}
	if r.trees == nil {
		r.trees = make(map[string]*node)
	}

	root := r.trees[method]
	if root == nil {
		root = new(node)
		r.trees[method] = root

		r.globalAllowed = r.allowed("*", "")
	}

	route := newRoute(path, handler, opts...)
	if route.name != "" {
		if _, ok := r.routes[route.name]; ok {
			panic("route name " + route.name + " is already registered")
		}
		if r.routes == nil {
			r.routes = make(map[string]*Route)
		}
		r.routes[route.name] = route
	}
	root.addRoute(path, route)

	// Update maxParams
	if pc := countParams(path); pc > r.maxParams {
		r.maxParams = pc
	}

	// Lazy-init paramsPool alloc func
	if r.paramsPool.New == nil && r.maxParams > 0 {
		r.paramsPool.New = func() interface{} {
			ps := make(Params, 0, r.maxParams)
			return &ps
		}
	}
}

// ServeFiles serves files from the given file system root.
// The path must end with "/*filepath", files are then served from the local
// path /defined/root/dir/*filepath.
// For example if root is "/etc" and *filepath is "passwd", the local file
// "/etc/passwd" would be served.
// Internally a http.FileServer is used, therefore http.NotFound is used instead
// of the Router's NotFound handler.
// To use the operating system's file system implementation,
// use http.Dir:
//     router.ServeFiles("/src/*filepath", http.Dir("/var/www"))
func (r *Router) ServeFiles(path string, root http.FileSystem) {
	if len(path) < 10 || path[len(path)-10:] != "/*filepath" {
		panic("path must end with /*filepath in path '" + path + "'")
	}

	fileServer := http.FileServer(root)

	r.Get(path, func(w http.ResponseWriter, req *http.Request) {
		req.URL.Path = GetParams(req).Get("filepath")
		fileServer.ServeHTTP(w, req)
	})
}

// Lookup allows the manual lookup of a method + path combo.
// This is e.g. useful to build a framework around this router.
// If the path was found, it returns the handle function and the path parameter
// values. Otherwise the third return value indicates whether a redirection to
// the same path with an extra / without the trailing slash should be performed.
func (r *Router) Lookup(method, path string) (*Route, Params, bool) {
	if root := r.trees[method]; root != nil {
		route, ps, tsr := root.getValue(path, r.getParams)
		if route == nil {
			return nil, nil, tsr
		}
		if ps == nil {
			return route, nil, tsr
		}
		return route, *ps, tsr
	}
	return nil, nil, false
}

func (r *Router) allowed(path, reqMethod string) (allow string) {
	allowed := make([]string, 0, 9)

	if path == "*" { // server-wide
		// empty method is used for internal calls to refresh the cache
		if reqMethod == "" {
			for method := range r.trees {
				if method == http.MethodOptions {
					continue
				}
				// Add request method to list of allowed methods
				allowed = append(allowed, method)
			}
		} else {
			return r.globalAllowed
		}
	} else { // specific path
		for method := range r.trees {
			// Skip the requested method - we already tried this one
			if method == reqMethod || method == http.MethodOptions {
				continue
			}

			handle, _, _ := r.trees[method].getValue(path, nil)
			if handle != nil {
				// Add request method to list of allowed methods
				allowed = append(allowed, method)
			}
		}
	}

	if len(allowed) > 0 {
		// Add request method to list of allowed methods
		allowed = append(allowed, http.MethodOptions)

		// Sort allowed methods.
		// sort.Strings(allowed) unfortunately causes unnecessary allocations
		// due to allowed being moved to the heap and interface conversion
		for i, l := 1, len(allowed); i < l; i++ {
			for j := i; j > 0 && allowed[j] < allowed[j-1]; j-- {
				allowed[j], allowed[j-1] = allowed[j-1], allowed[j]
			}
		}

		// return as comma separated list
		return strings.Join(allowed, ", ")
	}
	return
}

// ServeHTTP makes the router implement the http.Handler interface.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path

	if root := r.trees[req.Method]; root != nil {
		if route, ps, tsr := root.getValue(path, r.getParams); route != nil {
			if ps != nil {
				ctx := context.WithValue(req.Context(), paramsKey, *ps)
				req = req.WithContext(ctx)
				r.putParams(ps)
			}
			if r.SaveMatchedRoute {
				ctx := context.WithValue(req.Context(), routeKey, route)
				req = req.WithContext(ctx)
			}
			route.handler.ServeHTTP(w, req)
			return
		} else if req.Method != http.MethodConnect && path != "/" {
			// Moved Permanently, request with Get method
			code := http.StatusMovedPermanently
			if req.Method != http.MethodGet {
				// Permanent Redirect, request with same method
				code = http.StatusPermanentRedirect
			}

			if tsr && r.RedirectTrailingSlash {
				if len(path) > 1 && path[len(path)-1] == '/' {
					req.URL.Path = path[:len(path)-1]
				} else {
					req.URL.Path = path + "/"
				}
				http.Redirect(w, req, req.URL.String(), code)
				return
			}

			// Try to fix the request path
			if r.RedirectFixedPath {
				fixedPath, found := root.findCaseInsensitivePath(
					CleanPath(path),
					r.RedirectTrailingSlash,
				)
				if found {
					req.URL.Path = fixedPath
					http.Redirect(w, req, req.URL.String(), code)
					return
				}
			}
		}
	}

	if req.Method == http.MethodOptions && r.HandleOPTIONS {
		// Handle OPTIONS requests
		if allow := r.allowed(path, http.MethodOptions); allow != "" {
			w.Header().Set("Allow", allow)
			if r.GlobalOPTIONS != nil {
				r.GlobalOPTIONS.ServeHTTP(w, req)
			}
			return
		}
	} else if r.HandleMethodNotAllowed { // Handle 405
		if allow := r.allowed(path, req.Method); allow != "" {
			w.Header().Set("Allow", allow)
			if r.MethodNotAllowed != nil {
				r.MethodNotAllowed.ServeHTTP(w, req)
			} else {
				http.Error(w,
					http.StatusText(http.StatusMethodNotAllowed),
					http.StatusMethodNotAllowed,
				)
			}
			return
		}
	}

	// Handle 404
	if r.NotFound != nil {
		r.NotFound.ServeHTTP(w, req)
	} else {
		http.NotFound(w, req)
	}
}
