package health

import (
	"io/ioutil"
	"net/http"
	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus"
)

type checksHandler struct {
	lock sync.RWMutex

	livenessPath   string
	livenessChecks map[string]Check

	readinessPath   string
	readinessChecks map[string]Check

	// if true first found error will fail the check stage
	failFast bool
	logger   *logrus.Logger
}

// Checker ...
type Checker interface {
	AddLiveness(name string, check Check)
	AddReadiness(name string, check Check)
	Handler() http.Handler
	RegisterHandler(mux *http.ServeMux)
	SetFailFast(failFast bool)
	GetFailFast() bool
}

// NewchecksHandler accepts two strings: health and ready paths.
// These paths will be used for liveness and readiness checks.
func NewChecksHandler(healthPath, readyPath string) Checker {
	return newChecksHandler(healthPath, readyPath)
}

func newChecksHandler(healthPath, readyPath string) *checksHandler {
	if healthPath[0] != '/' {
		healthPath = "/" + healthPath
	}
	if readyPath[0] != '/' {
		readyPath = "/" + readyPath
	}
	ch := &checksHandler{
		livenessPath:    healthPath,
		livenessChecks:  map[string]Check{},
		readinessPath:   readyPath,
		readinessChecks: map[string]Check{},
		logger:          nil,
	}
	return ch
}

func NewChecksHandlerWithOptions(healthPath, readyPath string, options ...func(*checksHandler)) *checksHandler {
	ch := newChecksHandler(healthPath, readyPath)

	for _, option := range options {
		option(ch)
	}

	return ch
}

// SetFailFast sets failFast flag for failing on the first error found
func (ch *checksHandler) SetFailFast(failFast bool) {
	ch.failFast = failFast
}

func (ch *checksHandler) GetFailFast() bool {
	return ch.failFast
}

func WithLogger(logger *logrus.Logger) func(*checksHandler) {
	return func(c *checksHandler) {
		c.logger = logger
	}
}

func (ch *checksHandler) AddLiveness(name string, check Check) {
	ch.lock.Lock()
	defer ch.lock.Unlock()
	if ch.logger != nil {
		ch.logger.WithFields(logrus.Fields{
			"liveness_path":  ch.livenessPath,
			"readiness_path": ch.readinessPath,
			"name":           name,
		}).Warn("adding liveness check")
	}

	ch.livenessChecks[name] = check
}

func (ch *checksHandler) AddReadiness(name string, check Check) {
	ch.lock.Lock()
	defer ch.lock.Unlock()
	if ch.logger != nil {
		ch.logger.WithFields(logrus.Fields{
			"liveness_path":  ch.livenessPath,
			"readiness_path": ch.readinessPath,
			"name":           name,
		}).Warn("adding liveness check")
	}

	ch.readinessChecks[name] = check
}

// Handler returns a new http.Handler for the given health checker
func (ch *checksHandler) Handler() http.Handler {
	if ch.logger != nil {
		ch.logger.WithFields(logrus.Fields{
			"liveness_path":  ch.livenessPath,
			"readiness_path": ch.readinessPath,
		}).Warn("creating new ServeMux for health checker")
	}
	mux := http.NewServeMux()
	ch.registerMux(mux)
	return mux
}

// RegisterHandler registers the given health and readiness patterns onto the given http.ServeMux
func (ch *checksHandler) RegisterHandler(mux *http.ServeMux) {
	if ch.logger != nil {
		ch.logger.WithFields(logrus.Fields{
			"liveness_path":  ch.livenessPath,
			"readiness_path": ch.readinessPath,
		}).Warn("registering ServeMux for health checker")
	}
	ch.registerMux(mux)
}

func (ch *checksHandler) registerMux(mux *http.ServeMux) {
	if ch.logger != nil {
		ch.logger.WithFields(logrus.Fields{
			"liveness_path":  ch.livenessPath,
			"readiness_path": ch.readinessPath,
		}).Warn("registering endpoints for health checker")
	}
	mux.HandleFunc(ch.readinessPath, ch.readyEndpoint)
	mux.HandleFunc(ch.livenessPath, ch.healthEndpoint)
}

func (ch *checksHandler) healthEndpoint(rw http.ResponseWriter, r *http.Request) {
	ch.handle(rw, r, ch.livenessChecks)
}

func (ch *checksHandler) readyEndpoint(rw http.ResponseWriter, r *http.Request) {
	ch.handle(rw, r, ch.readinessChecks)
}

func checkLogger(logger *logrus.Entry) (*logrus.Entry, bool) {
	if logger == nil {
		return logger, false
	} else if logger.Logger.Out == ioutil.Discard {
		return logger, false
	}
	return logger, true
}

func (ch *checksHandler) handle(rw http.ResponseWriter, r *http.Request, checksSets ...map[string]Check) {
	logger := ch.logger
	ctxLogger, ok := checkLogger(ctxlogrus.Extract(r.Context()))
	if ok {
		logger = ctxLogger.Logger
	}

	if r.Method != http.MethodGet {
		if logger != nil {
			logger.WithFields(logrus.Fields{
				"liveness_path":  ch.livenessPath,
				"readiness_path": ch.readinessPath,
				"url":            r.URL.RawPath,
			}).Warn("received non-GET request")
		}
		http.Error(rw, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	errors := map[string]error{}
	status := http.StatusOK
	ch.lock.RLock()
	defer ch.lock.RUnlock()

	for _, checks := range checksSets {
		for name, check := range checks {
			if check == nil {
				continue
			}
			if err := check(); err != nil {
				if logger != nil {
					logger.WithFields(logrus.Fields{
						"liveness_path":  ch.livenessPath,
						"readiness_path": ch.readinessPath,
						"url":            r.URL.RawPath,
					}).WithError(err).Error("health check returned error")
				}
				status = http.StatusServiceUnavailable
				errors[name] = err
				if ch.failFast {
					rw.WriteHeader(status)
					return
				}
			}
		}
	}
	rw.WriteHeader(status)

	return

	// Uncomment to write errors and get non-empty response
	// rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	// if status == http.StatusOK {
	// 	rw.Write([]byte("{}\n"))
	// } else {
	// 	encoder := json.NewEncoder(rw)
	// 	encoder.SetIndent("", "    ")
	// 	encoder.Encode(errors)
	// }
}
