package router

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/services/eventbridge"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/domainregistry"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/inithooks"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/services/acm"
	"github.com/Neaox/overcast/internal/services/apigateway"
	"github.com/Neaox/overcast/internal/services/appconfig"
	"github.com/Neaox/overcast/internal/services/appconfigdata"
	"github.com/Neaox/overcast/internal/services/appregistry"
	"github.com/Neaox/overcast/internal/services/appsync"
	"github.com/Neaox/overcast/internal/services/athena"
	"github.com/Neaox/overcast/internal/services/autoscaling"
	"github.com/Neaox/overcast/internal/services/backup"
	"github.com/Neaox/overcast/internal/services/bedrock"
	"github.com/Neaox/overcast/internal/services/cloudformation"
	"github.com/Neaox/overcast/internal/services/cloudfront"
	"github.com/Neaox/overcast/internal/services/cloudtrail"
	"github.com/Neaox/overcast/internal/services/cloudwatch"
	"github.com/Neaox/overcast/internal/services/cloudwatch/logs"
	"github.com/Neaox/overcast/internal/services/cognito"
	"github.com/Neaox/overcast/internal/services/dynamodb"
	"github.com/Neaox/overcast/internal/services/dynamodbstreams"
	"github.com/Neaox/overcast/internal/services/ec2"
	"github.com/Neaox/overcast/internal/services/ecr"
	"github.com/Neaox/overcast/internal/services/ecs"
	"github.com/Neaox/overcast/internal/services/eks"
	"github.com/Neaox/overcast/internal/services/elasticache"
	elbv2svcpkg "github.com/Neaox/overcast/internal/services/elbv2"
	"github.com/Neaox/overcast/internal/services/firehose"
	"github.com/Neaox/overcast/internal/services/glue"
	"github.com/Neaox/overcast/internal/services/iam"
	"github.com/Neaox/overcast/internal/services/kinesis"
	"github.com/Neaox/overcast/internal/services/kms"
	"github.com/Neaox/overcast/internal/services/lambda"
	"github.com/Neaox/overcast/internal/services/msk"
	"github.com/Neaox/overcast/internal/services/opensearch"
	"github.com/Neaox/overcast/internal/services/organizations"
	"github.com/Neaox/overcast/internal/services/pipes"
	"github.com/Neaox/overcast/internal/services/rds"
	route53svcpkg "github.com/Neaox/overcast/internal/services/route53"
	"github.com/Neaox/overcast/internal/services/s3"
	"github.com/Neaox/overcast/internal/services/scheduler"
	"github.com/Neaox/overcast/internal/services/secretsmanager"
	"github.com/Neaox/overcast/internal/services/ses"
	"github.com/Neaox/overcast/internal/services/shield"
	"github.com/Neaox/overcast/internal/services/sns"
	"github.com/Neaox/overcast/internal/services/sqs"
	"github.com/Neaox/overcast/internal/services/ssm"
	"github.com/Neaox/overcast/internal/services/stepfunctions"
	"github.com/Neaox/overcast/internal/services/sts"
	"github.com/Neaox/overcast/internal/services/transfer"
	"github.com/Neaox/overcast/internal/services/waf"
	"github.com/Neaox/overcast/internal/smtp"
	"github.com/Neaox/overcast/internal/state"
)

// New builds and returns the root HTTP handler for the emulator, plus a
// cleanup function that should be called during graceful shutdown with the
// remaining shutdown context. It stops background service resources (e.g. the
// Lambda Runtime API server) and closes any other open handles (e.g. the SMTP
// listener).
//
// The returned preShutdown function must be called BEFORE http.Server.Shutdown
// to unblock long-lived handlers (e.g. the SSE /_events endpoint).
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock, hookRunner ...*inithooks.Runner) (handler http.Handler, preShutdown func(), cleanup func(context.Context), waitReady func()) {
	prof := newStartupProfiler()

	r := chi.NewRouter()
	var cleanups []func()

	// shutdownCh is closed during pre-shutdown to unblock long-lived handlers
	// (e.g. the SSE /_events endpoint) so http.Server.Shutdown can complete
	// without waiting for the full ShutdownTimeout.
	shutdownCh := make(chan struct{})
	preShutdown = sync.OnceFunc(func() { close(shutdownCh) })

	// smtpCtx is cancelled during shutdown so the SMTP server's Accept
	// loop exits promptly without waiting for context.Background().
	smtpCtx, smtpCancel := context.WithCancel(context.Background())
	cleanups = append(cleanups, smtpCancel)

	// ---- Event bus (declared early so middleware can reference it) ----------
	// The bus pointer is set below, after middleware registration.
	// middleware.RequestEvents dereferences it at request time (always after bus
	// creation since http.Server.Serve is not called until New returns).
	var bus *events.Bus

	// ---- Middleware chain --------------------------------------------------
	r.Use(chimiddleware.RealIP)
	r.Use(middleware.CORS)
	r.Use(middleware.DrainBody)
	r.Use(middleware.S3VirtualHostFor(cfg.Hostname))
	r.Use(middleware.RequestID)
	r.Use(middleware.Recovery(logger))
	r.Use(middleware.Logger(logger, clk))
	// NotReady short-circuits with a 503 while the storage backend is still
	// completing a one-time startup migration (storage-plan.md item — see
	// internal/middleware/notready.go) — placed after Logger so a rejected
	// request is still observable in logs, and before every other
	// middleware below so none of that work (event recording, SigV4, IAM,
	// region/protocol detection) runs for a request about to be rejected
	// anyway.
	r.Use(middleware.NotReady(store))
	r.Use(middleware.RequestEvents(&bus, clk))
	r.Use(middleware.SigV4(cfg.SigV4Validate, middleware.NewSecretResolver(store), logger, clk))
	r.Use(middleware.IAMEnforce(cfg.EnforceIAM, store, logger))
	r.Use(middleware.Region)
	// Protocol-detection middleware (Smithy alignment, see
	// docs/plans/smithy.md). Always-on as of Phase 6 completion.
	r.Use(middleware.Protocol(codec.DefaultIdentifiers()))
	// queryGetMiddleware must be registered here, before any route is added
	// (chi requirement). It intercepts GET /?Action=... requests (e.g. SNS
	// UnsubscribeURL) letting S3's GET / handle everything else. The pointer
	// is read at request time, so the slice is fully populated by then.
	var queryDispatchers []QueryDispatcher
	r.Use(queryGetMiddleware(&queryDispatchers))
	prof.mark("middleware chain")

	// ---- Internal endpoints (always available) ----------------------------
	// healthHandler is registered after the service loop so it can include
	// the list of enabled service names and their emulation tiers in the response.
	var enabledServiceNames []string
	enabledTiers := make(map[string]string)
	// Suppress noisy 404s when browsers auto-request /favicon.ico.
	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// ---- Init hook status (LocalStack-compatible, always available) --------
	var initRunner *inithooks.Runner
	if len(hookRunner) > 0 {
		initRunner = hookRunner[0]
	}
	r.Get("/_overcast/init", initStatusHandler(initRunner))
	r.Get("/_overcast/init/{stage}", initStageStatusHandler(initRunner))

	// ---- Debug dependencies ---------------------------------------------------
	// ec2Svc is constructed before the rest of the services because the debug
	// namespace exposes EC2 internals (/_debug/ec2/vpcs). Its constructor has no
	// startup side-effects — see docs/performance.md § Startup budget.
	ec2Svc := ec2.New(cfg, store, logger, clk)

	// ---- Event bus (create) ------------------------------------------------
	bus = events.NewBus()

	// ---- SSE event stream (always available) --------------------------------
	// GET /_events?source=s3 — streams all internal events to connected clients.
	r.Get("/_events", eventsHandler(bus, logger, shutdownCh))

	// ---- Custom domain registry (always available) -------------------------
	// Tracks API Gateway / CloudFront custom domain names and streams changes
	// to the host CLI (overcast dev) so it can drive mDNS publishing.
	domainReg := domainregistry.New()
	r.Get("/_internal/domains/watch", domainsWatchHandler(domainReg, logger, shutdownCh))

	// ---- Runtime metrics (always available) ---------------------------------
	// GET /_metrics — returns a snapshot of Go runtime memory/GC/goroutine stats.
	r.Get("/_metrics", metricsHandler())

	// ---- Server info (always available) -------------------------------------
	// GET /_/info — returns the server's configured region and account ID.
	// Used by the web UI to seed the region selector on first load.
	r.Get("/_/info", newInfoHandler(cfg))
	prof.mark("bus + SSE + internal routes")

	// Smithy RPC v2 CBOR requests use /service/{Service}/operation/{Operation}.
	// Register this before service routes so S3's wildcard cannot steal the path.
	smithyDispatchers := make(map[string]TargetDispatcher)
	r.Post("/service/{service}/operation/{operation}", smithyRPCDispatch(smithyDispatchers))

	// MCP runtime routes are mounted through a build-tag-aware hook.
	// Slim builds intentionally do not expose MCP endpoints.
	registerMCPRoutes(r, cfg, store, bus, logger)

	// ---- Service registry -------------------------------------------------
	// To add a new service: implement router.Service and append it here.
	prof.mark("MCP routes + internal endpoints")
	s3Svc := s3.New(cfg, store, logger, clk, bus)
	prof.mark("  new: s3")
	sqsSvc := sqs.New(cfg, store, logger, clk)
	prof.mark("  new: sqs")
	snsSvc := sns.New(cfg, store, logger, clk)
	prof.mark("  new: sns")
	sesSvc := ses.New(cfg, store, logger, clk)
	prof.mark("  new: ses")
	ddbSvc := dynamodb.New(cfg, store, logger, clk, bus)
	prof.mark("  new: dynamodb")
	// logsSvc is constructed here (ahead of its usual place in the service
	// registry order below) because the debug namespace below needs it as a
	// DebugStateProvider (its events live in a dedicated logs_events SQL
	// table — see storage-plan.md 2.3 — invisible to /_debug/state without
	// this). Its constructor has no startup side-effects, same as ec2Svc
	// above — see docs/performance.md § Startup budget.
	logsSvc := logs.New(cfg, store, logger, clk)
	prof.mark("  new: logs")
	if cfg.Debug {
		var ec2Debug debugEC2Provider
		if cfg.Services["ec2"] {
			ec2Debug = ec2Svc
		}
		var debugProviders []DebugStateProvider
		if cfg.Services["dynamodb"] {
			debugProviders = append(debugProviders, ddbSvc)
		}
		if cfg.Services["logs"] {
			debugProviders = append(debugProviders, logsSvc)
		}
		r.Route("/_debug", debugHandlers(cfg, store, ec2Debug, debugProviders))
	}
	lambdaSvc := lambda.New(cfg, store, logger, clk)
	prof.mark("  new: lambda")
	pipesSvc := pipes.New(cfg, store, logger, clk)
	prof.mark("  new: pipes")
	smSvc := secretsmanager.New(cfg, store, logger, clk)
	prof.mark("  new: secretsmanager")
	stsSvc := sts.New(cfg, store, logger, clk)
	prof.mark("  new: sts")
	ssmSvc := ssm.New(cfg, store, logger, clk)
	prof.mark("  new: ssm")
	kmsSvc := kms.New(cfg, store, logger, clk)
	prof.mark("  new: kms")
	iamSvc := iam.New(cfg, store, logger, clk)
	prof.mark("  new: iam")
	cfnSvc := cloudformation.New(cfg, store, logger, clk)
	prof.mark("  new: cloudformation")
	rdsSvc := rds.New(cfg, store, logger, clk)
	prof.mark("  new: rds")
	ecsSvc := ecs.New(cfg, store, logger, clk)
	prof.mark("  new: ecs")
	cognitoSvc := cognito.New(cfg, store, logger, clk)
	prof.mark("  new: cognito")
	sfnSvc := stepfunctions.New(cfg, store, logger, clk)
	prof.mark("  new: stepfunctions")
	wafSvc := waf.New(cfg, store, logger, clk)
	prof.mark("  new: waf")
	shieldSvc := shield.New(cfg, store, logger, clk)
	prof.mark("  new: shield")
	appsyncSvc := appsync.New(cfg, store, logger, clk)
	prof.mark("  new: appsync")
	apigwSvc := apigateway.New(cfg, store, logger, clk)
	prof.mark("  new: apigateway")
	cloudfrontSvc := cloudfront.New(cfg, store, logger, clk)
	prof.mark("  new: cloudfront")
	ebSvc := eventbridge.New(cfg, store, logger, clk)
	prof.mark("  new: eventbridge")
	kinesisSvc := kinesis.New(cfg, store, logger, clk)
	prof.mark("  new: kinesis")
	appregistrySvc := appregistry.New(cfg, store, logger, clk)
	prof.mark("  new: appregistry")
	cloudwatchSvc := cloudwatch.New(cfg, store, logger, clk)
	prof.mark("  new: cloudwatch")
	acmSvc := acm.New(cfg, store, logger, clk)
	prof.mark("  new: acm")
	opensearchSvc := opensearch.New(cfg, store, logger, clk)
	prof.mark("  new: opensearch")
	appconfigSvc := appconfig.New(cfg, store, logger, clk)
	prof.mark("  new: appconfig")
	appconfigdataSvc := appconfigdata.New(cfg, store, logger, clk, appconfigSvc)
	prof.mark("  new: appconfigdata")
	bedrockSvc := bedrock.New(cfg, store, logger, clk)
	prof.mark("  new: bedrock")
	glueSvc := glue.New(cfg, store, logger, clk)
	prof.mark("  new: glue")
	firehoseSvc := firehose.New(cfg, store, logger, clk)
	prof.mark("  new: firehose")
	athenaSvc := athena.New(cfg, store, logger, clk)
	prof.mark("  new: athena")
	elasticacheSvc := elasticache.New(cfg, store, logger, clk)
	prof.mark("  new: elasticache")
	mskSvc := msk.New(cfg, store, logger, clk)
	prof.mark("  new: msk")
	ecrSvc := ecr.New(cfg, store, logger, clk)
	prof.mark("  new: ecr")
	eksSvc := eks.New(cfg, store, logger, clk)
	prof.mark("  new: eks")

	schedulerSvc := scheduler.New(cfg, store, logger, clk)
	prof.mark("  new: scheduler")
	route53Svc := route53svcpkg.New(cfg, store, logger, clk)
	prof.mark("  new: route53")
	elbv2Svc := elbv2svcpkg.New(cfg, store, logger, clk)
	prof.mark("  new: elbv2")
	organizationsSvc := organizations.New(cfg, store, logger, clk)
	prof.mark("  new: organizations")
	autoScalingSvc := autoscaling.New(cfg, store, logger, clk)
	prof.mark("  new: autoscaling")
	cloudTrailSvc := cloudtrail.New(cfg, store, logger, clk)
	prof.mark("  new: cloudtrail")
	backupSvc := backup.New(cfg, store, logger, clk)
	prof.mark("  new: backup")
	transferSvc := transfer.New(cfg, store, logger, clk)
	prof.mark("  new: transfer")

	prof.mark("service constructors (47)")

	allServices := []Service{
		// S3 is listed last so its /{bucket}/* wildcard routes are registered
		// after all other services, preventing it from shadowing /_<service> paths.
		sqsSvc,
		ddbSvc,
		snsSvc,
		sesSvc,
		lambdaSvc,
		dynamodbstreams.New(ddbSvc, logger),
		pipesSvc,
		logsSvc,
		smSvc,
		stsSvc,
		ssmSvc,
		kmsSvc,
		iamSvc,
		cfnSvc,
		ec2Svc,
		rdsSvc,
		ecsSvc,
		cognitoSvc,
		sfnSvc,
		wafSvc,
		shieldSvc,
		appsyncSvc,
		apigwSvc,
		cloudfrontSvc,
		ebSvc,
		kinesisSvc,
		appregistrySvc,
		cloudwatchSvc,
		acmSvc,
		opensearchSvc,
		appconfigSvc,
		appconfigdataSvc,
		bedrockSvc,
		glueSvc,
		firehoseSvc,
		athenaSvc,
		elasticacheSvc,
		mskSvc,
		ecrSvc,
		eksSvc,
		schedulerSvc,
		route53Svc,
		elbv2Svc,
		organizationsSvc,
		autoScalingSvc,
		cloudTrailSvc,
		backupSvc,
		transferSvc,
		s3Svc, // must be last — registers /{bucket}/* wildcard
	}

	// Collect target dispatchers for services that share POST /.
	var dispatchers []TargetDispatcher
	// disabledTargetPrefixes collects X-Amz-Target prefixes of known-but-disabled
	// TargetDispatcher services so targetDispatch can return ServiceDisabled
	// instead of UnknownOperationException.
	var disabledTargetPrefixes []string
	// queryDispatchers is declared above, near middleware registration.
	type namedStopper struct {
		name string
		Stopper
	}
	var stoppers []namedStopper
	var readiers []Readier
	serviceByName := make(map[string]Service, len(allServices))

	for _, svc := range allServices {
		if !cfg.Services[svc.Name()] {
			logger.Debug("service disabled", zap.String("service", svc.Name()))
			if pp, ok := svc.(PathPrefixService); ok {
				for _, prefix := range pp.PathPrefixes() {
					r.HandleFunc(prefix, serviceDisabledHandler)
					r.HandleFunc(prefix+"/*", serviceDisabledHandler)
				}
			}
			if td, ok := svc.(TargetDispatcher); ok {
				if p := td.TargetPrefix(); p != "" {
					disabledTargetPrefixes = append(disabledTargetPrefixes, p)
				}
			}
			continue
		}
		serviceByName[svc.Name()] = svc
		svc.RegisterRoutes(r)
		prof.mark("  routes: " + svc.Name())
		if td, ok := svc.(TargetDispatcher); ok {
			dispatchers = append(dispatchers, td)
		}
		if _, ok := svc.(ProtocolService); ok {
			if td, ok := svc.(TargetDispatcher); ok {
				smithyDispatchers[strings.ToLower(svc.Name())] = td
				if prefix := strings.TrimSuffix(td.TargetPrefix(), "."); prefix != "" {
					smithyDispatchers[strings.ToLower(prefix)] = td
				}
			}
		}
		if qd, ok := svc.(QueryDispatcher); ok {
			queryDispatchers = append(queryDispatchers, qd)
		}
		if st, ok := svc.(Stopper); ok {
			stoppers = append(stoppers, namedStopper{name: svc.Name(), Stopper: st})
		}
		if rd, ok := svc.(Readier); ok {
			readiers = append(readiers, rd)
		}
		enabledServiceNames = append(enabledServiceNames, svc.Name())
		if tier, ok := ServiceTiers[svc.Name()]; ok {
			enabledTiers[svc.Name()] = tier
		} else {
			enabledTiers[svc.Name()] = TierStub
		}
		logger.Info("service enabled", zap.String("service", svc.Name()))
	}

	prof.mark("RegisterRoutes for enabled services")

	// ---- Event notification wiring ----------------------------------------
	// S3 notifications → SQS + Lambda: connect after all services are constructed.
	if cfg.Services["s3"] && cfg.Services["sqs"] {
		var lambdaInvoker events.FunctionInvoker
		if cfg.Services["lambda"] {
			lambdaInvoker = lambdaSvc.Invoker()
		}
		s3Svc.InitNotifications(sqsSvc.Enqueuer(), lambdaInvoker, bus, logger)
	}
	// Lambda → CloudWatch Logs: wire log writer so Lambda can write invocation logs.
	if cfg.Services["lambda"] && cfg.Services["logs"] {
		lambdaSvc.InitLogWriter(logsSvc.LogWriter())
	}
	// Lambda bus: lifecycle events for topology / UI.
	if cfg.Services["lambda"] {
		lambdaSvc.InitBus(bus)
	}
	// Lambda ← S3: reactive code sync — when a function's code object is
	// updated in S3, automatically refresh CodeZip and invalidate the warm pool
	// so the next invoke picks up the new code without a redeploy.
	if cfg.Services["lambda"] && cfg.Services["s3"] {
		lambdaSvc.InitS3Sync(func(ctx context.Context, bucket, key string) ([]byte, error) {
			return s3Svc.GetObjectBytes(ctx, bucket, key)
		})
	}
	// Lambda → EC2: VPC resolver so Lambda can connect containers to VPC networks.
	if cfg.Services["lambda"] && cfg.Services["ec2"] {
		lambdaSvc.SetVPCResolver(ec2Svc)
	}
	// ECS/RDS → EC2: resolve subnet-backed launches against VPC network state.
	if cfg.Services["ecs"] && cfg.Services["ec2"] {
		ecsSvc.SetVPCResolver(ec2Svc)
	}
	if cfg.Services["rds"] && cfg.Services["ec2"] {
		rdsSvc.SetVPCResolver(ec2Svc)
	}
	// SNS: wire lifecycle/publish events for topology / UI.
	if cfg.Services["sns"] {
		snsSvc.InitBus(bus)
	}
	// SNS → SQS: wire enqueuer for Publish fan-out.
	if cfg.Services["sns"] && cfg.Services["sqs"] {
		snsSvc.InitSQSDelivery(sqsSvc.Enqueuer())
	}
	// SNS/SES → email: wire SMTP mailer. The mock capture server (if enabled) is
	// started as a background goroutine; its lifecycle is tied to the process.
	if cfg.Services["sns"] || cfg.Services["ses"] || cfg.Services["cognito"] {
		mailStore := smtp.NewMailStore(cfg.SMTPInboxMax)

		// publishInbox fires the inbox SSE event for any captured message
		// (email or SMS). Defined once and shared by both transport callbacks.
		publishInbox := func(m *smtp.CapturedMessage) {
			bus.Publish(context.Background(), events.Event{
				Type:   events.InboxDelivered,
				Time:   m.ReceivedAt,
				Source: "inbox",
				Payload: events.InboxDeliveredPayload{
					ID:      m.ID,
					From:    m.From,
					To:      m.To,
					Subject: m.Subject,
				},
			})
		}

		// SMS capture is always available — it writes directly into the store.
		smsSender := smtp.NewMockSMSSender(mailStore)
		smsSender.OnMessage = publishInbox
		if cfg.Services["sns"] {
			snsSvc.InitSMSDelivery(smsSender)
		}
		if cfg.Services["cognito"] {
			cognitoSvc.InitSMSDelivery(smsSender)
		}

		// Webhook + push capture for SNS http/https and application subscriptions.
		if cfg.Services["sns"] {
			outbound := smtp.NewMockOutboundCapture(mailStore, publishInbox)
			snsSvc.InitOutboundCapture(outbound)
		}

		// Expose the inbox capture API under /_overcast/inbox/.
		r.Route("/_overcast/inbox", inboxHandlers(mailStore))

		var mailerHost string
		var mailerPort int
		if cfg.SMTPMock {
			// Bind and serve in the background so startup is not blocked.
			smtpAddr := fmt.Sprintf("127.0.0.1:%d", cfg.SMTPPort)
			smtpSrv := smtp.NewServer(smtpAddr, mailStore)
			smtpSrv.OnMessage = publishInbox

			// lazyMailer will block the first Send until the SMTP server is ready.
			lm := smtp.NewLazyMailer()
			cleanups = append(cleanups, func() { smtpSrv.Close() })

			go func() {
				boundAddr, err := smtpSrv.Listen()
				if err != nil {
					logger.Error("smtp mock server: failed to bind", zap.Error(err))
					return
				}
				go smtpSrv.Serve(smtpCtx)

				host, portStr, _ := net.SplitHostPort(boundAddr)
				port, _ := strconv.Atoi(portStr)
				lm.SetReady(smtp.NewMailer(smtp.Config{
					Host: host,
					Port: port,
				}))
				logger.Info("smtp mock server started", zap.String("addr", boundAddr))
			}()

			if cfg.Services["sns"] {
				snsSvc.InitEmailDelivery(lm)
			}
			if cfg.Services["ses"] {
				sesSvc.InitEmailDelivery(lm)
			}
			if cfg.Services["cognito"] {
				cognitoSvc.InitEmailDelivery(lm)
			}
		} else {
			mailerHost = cfg.SMTPHost
			mailerPort = cfg.SMTPPort
		}
		if mailerHost != "" {
			mailer := smtp.NewMailer(smtp.Config{
				Host:     mailerHost,
				Port:     mailerPort,
				Username: cfg.SMTPUsername,
				Password: cfg.SMTPPassword,
				TLS:      cfg.SMTPTLS,
			})
			if cfg.Services["sns"] {
				snsSvc.InitEmailDelivery(mailer)
			}
			if cfg.Services["ses"] {
				sesSvc.InitEmailDelivery(mailer)
			}
			if cfg.Services["cognito"] {
				cognitoSvc.InitEmailDelivery(mailer)
			}
		}
	}
	// SQS: wire bus for queue lifecycle events (topology map).
	if cfg.Services["sqs"] {
		sqsSvc.InitBus(bus)
	}
	// CloudWatch Logs: wire bus for log group lifecycle events.
	if cfg.Services["logs"] {
		logsSvc.InitBus(bus)
	}
	// IAM: wire bus for role/user/policy lifecycle events.
	if cfg.Services["iam"] {
		iamSvc.InitBus(bus)
	}
	// STS: wire bus for assume-role events.
	if cfg.Services["sts"] {
		stsSvc.InitBus(bus)
	}
	// SSM: wire bus for parameter lifecycle events.
	if cfg.Services["ssm"] {
		ssmSvc.InitBus(bus)
	}
	// KMS: wire bus for key lifecycle events.
	if cfg.Services["kms"] {
		kmsSvc.InitBus(bus)
	}
	// Secrets Manager: wire bus for secret lifecycle events.
	if cfg.Services["secretsmanager"] {
		smSvc.InitBus(bus)
	}
	// SES: wire bus for email/identity/template events.
	if cfg.Services["ses"] {
		sesSvc.InitBus(bus)
	}
	// Kinesis: wire bus for stream lifecycle events.
	if cfg.Services["kinesis"] {
		kinesisSvc.InitBus(bus)
	}
	// RDS: wire bus so Docker container events update instance status.
	if cfg.Services["rds"] {
		rdsSvc.InitBus(bus)
	}
	// ECS: wire bus so Docker container events update task status.
	if cfg.Services["ecs"] {
		ecsSvc.InitBus(bus)
	}
	// ElastiCache: wire bus so Docker container events update cluster status.
	if cfg.Services["elasticache"] {
		elasticacheSvc.InitBus(bus)
	}
	// ECR: wire bus for repository lifecycle events.
	if cfg.Services["ecr"] {
		ecrSvc.InitBus(bus)
	}
	// MSK: wire bus so Docker container events update cluster status.
	if cfg.Services["msk"] {
		mskSvc.InitBus(bus)
	}
	// EC2: wire bus for VPC/subnet/security group lifecycle events.
	if cfg.Services["ec2"] {
		ec2Svc.InitBus(bus)
	}
	// EventBridge: wire bus for bus/rule lifecycle events.
	if cfg.Services["eventbridge"] {
		ebSvc.InitBus(bus)
		ebSvc.InitRouter(r)
	}
	// Step Functions: wire bus for state machine/execution lifecycle events.
	if cfg.Services["stepfunctions"] {
		sfnSvc.InitBus(bus)
	}
	// AppSync: wire bus for API lifecycle events.
	if cfg.Services["appsync"] {
		appsyncSvc.InitBus(bus)
		if cfg.Services["lambda"] {
			appsyncSvc.InitLambdaInvoker(lambdaSvc.SyncInvoker())
		}
		if cfg.Services["dynamodb"] {
			appsyncSvc.InitDynamoDBInvoker(ddbSvc.DynamoDBInvoker())
		}
	}
	// CloudFront: wire bus for distribution lifecycle events.
	if cfg.Services["cloudfront"] {
		cloudfrontSvc.InitBus(bus)
	}
	// CloudFormation: wire bus + router for async provisioning.
	if cfg.Services["cloudformation"] {
		cfnSvc.InitBus(bus)
	}
	// API Gateway: wire bus + Lambda invoker for proxy execution.
	if cfg.Services["apigateway"] {
		apigwSvc.InitBus(bus)
		apigwSvc.InitDomainRegistry(domainReg)
		if cfg.Services["lambda"] {
			apigwSvc.InitLambdaInvoker(lambdaSvc.SyncInvoker())
		}
		if cfg.Services["cognito"] {
			apigwSvc.InitCognitoValidator(cognitoSvc)
		}
	}
	// DynamoDB Streams → SQS via Pipes: subscribe to stream events and enqueue.
	if cfg.Services["pipes"] && cfg.Services["dynamodb"] && cfg.Services["sqs"] {
		pipesSvc.InitDelivery(bus, sqsSvc.Enqueuer())
	}
	if cfg.Services["scheduler"] {
		ti := scheduler.TargetInvoker{}
		if cfg.Services["lambda"] {
			ti.Lambda = lambdaSvc.Invoker()
		}
		if cfg.Services["sqs"] {
			ti.SQS = sqsSvc.Enqueuer()
		}
		schedulerSvc.InitTargets(ti)
	}
	// ---- Docker Supervisor ------------------------------------------------
	// A single Supervisor probes Docker once per unique socket, creates per-
	// service networks, runs one event watcher, and reconciles container state.
	// Services that need Docker (Lambda, RDS, ECS) are wired in a single
	// background goroutine so startup is not blocked if Docker is slow.
	// Lambda manages its own client/runtime but still benefits from the shared
	// watcher (container lifecycle events on the bus).
	dockerServices := map[string]docker.ServiceConfig{}
	dockerSetters := map[string]func(*docker.Client){} // name → SetDocker callback
	if cfg.Services["lambda"] {
		// Lambda probes Docker independently (it needs the Runtime API server);
		// we register its socket/network so the Supervisor starts a watcher.
		dockerServices["lambda"] = docker.ServiceConfig{Name: "lambda", Socket: cfg.LambdaDockerSocket, Network: cfg.LambdaNetwork}
	}
	if cfg.Services["rds"] {
		dockerServices["rds"] = docker.ServiceConfig{Name: "rds", Socket: cfg.RDSDockerSocket, Network: cfg.RDSNetwork}
		dockerSetters["rds"] = rdsSvc.SetDocker
	}
	if cfg.Services["elasticache"] {
		dockerServices["elasticache"] = docker.ServiceConfig{Name: "elasticache", Socket: cfg.ElastiCacheDockerSocket, Network: cfg.ElastiCacheNetwork}
		dockerSetters["elasticache"] = elasticacheSvc.SetDocker
	}
	if cfg.Services["msk"] {
		dockerServices["msk"] = docker.ServiceConfig{Name: "msk", Socket: cfg.MSKDockerSocket, Network: cfg.MSKNetwork}
		dockerSetters["msk"] = mskSvc.SetDocker
	}
	if cfg.Services["ecs"] {
		dockerServices["ecs"] = docker.ServiceConfig{Name: "ecs", Socket: cfg.ECSDockerSocket, Network: cfg.ECSNetwork}
		dockerSetters["ecs"] = ecsSvc.SetDocker
	}
	if cfg.Services["ec2"] {
		// EC2 manages its own networks (one per VPC) — empty Network skips
		// static network creation in the Supervisor probe.
		dockerServices["ec2"] = docker.ServiceConfig{Name: "ec2", Socket: cfg.LambdaDockerSocket, Network: ""}
		dockerSetters["ec2"] = ec2Svc.SetDocker
	}
	if cfg.Services["eks"] && cfg.EKSMode == config.EKSModeLive && cfg.EKSDockerSocket != "" {
		dockerServices["eks"] = docker.ServiceConfig{Name: "eks", Socket: cfg.EKSDockerSocket, Network: cfg.EKSNetwork}
		dockerSetters["eks"] = eksSvc.SetDocker
	}
	if len(dockerServices) > 0 {
		dockerSup := docker.NewSupervisor(bus, logger)
		cleanups = append(cleanups, dockerSup.Close)

		go func() {
			// Collect configs in deterministic order.
			var configs []docker.ServiceConfig
			for _, name := range []string{"lambda", "rds", "elasticache", "msk", "ecs", "ec2", "eks"} {
				if sc, ok := dockerServices[name]; ok {
					configs = append(configs, sc)
				}
			}

			results := dockerSup.Probe(context.Background(), configs)

			// Wire each successful service and reconcile containers/networks.
			for _, res := range results {
				if setter, ok := dockerSetters[res.Name]; ok {
					setter(res.Client)
				}
				reconcileDockerContainers(context.Background(), res.Client, res.Name, serviceByName[res.Name], logger)
				reconcileDockerNetworks(context.Background(), res.Client, res.Name, serviceByName[res.Name], logger)
			}

			// Start one watcher per unique Docker daemon (blocks until shutdown).
			dockerSup.Run(context.Background())
		}()
	}
	// SQS/DynamoDB Streams → Lambda via event source mappings.
	if cfg.Services["lambda"] {
		var receiver events.MessageReceiver
		var enqueuer events.MessageEnqueuer
		if cfg.Services["sqs"] {
			receiver = sqsSvc.Receiver()
			enqueuer = sqsSvc.Enqueuer()
		}
		lambdaSvc.InitESMDelivery(receiver, enqueuer, bus)
	}

	// Build goal tier map for enabled services.
	enabledGoalTiers := make(map[string]string, len(enabledServiceNames))
	for _, name := range enabledServiceNames {
		if goal, ok := ServiceGoalTiers[name]; ok {
			enabledGoalTiers[name] = goal
		} else if current, ok := ServiceTiers[name]; ok {
			// No explicit goal: goal == current tier (already where we want to be)
			enabledGoalTiers[name] = current
		}
	}

	// ---- /v2/apis service dispatch ----------------------------------------
	// Both API Gateway v2 and AppSync Events API register routes under /v2/apis.
	// In real AWS these live on different hostnames; in this emulator they share
	// one listener. We disambiguate using the SigV4 credential scope service
	// name ("apigateway" vs "appsync"). If neither service is enabled, or if
	// the credential scope cannot be parsed, we fall back to API Gateway (the
	// more commonly used service at this path).
	{
		var apigwV2Router, appsyncEventsRouter http.Handler
		if cfg.Services["apigateway"] {
			apigwV2Router = apigwSvc.V2APIRouter()
		}
		if cfg.Services["appsync"] {
			appsyncEventsRouter = appsyncSvc.EventsAPIRouter()
		}
		if apigwV2Router != nil || appsyncEventsRouter != nil {
			r.Route("/v2/apis", func(sub chi.Router) {
				sub.HandleFunc("/*", v2APIsDispatch(apigwV2Router, appsyncEventsRouter))
				sub.HandleFunc("/", v2APIsDispatch(apigwV2Router, appsyncEventsRouter))
			})
		}
	}

	// ---- /v1/tags service dispatch -----------------------------------------
	// AppSync and MSK both expose TagResource/UntagResource/ListTagsForResource
	// at /v1/tags/{resourceArn}. Unlike /v2/apis above, the path here carries
	// the resourceArn itself, which is self-describing — its
	// "arn:{partition}:{service}:..." shape names the owning service
	// directly — so we dispatch on that instead of the SigV4 credential
	// scope. This also means a malformed or foreign-service ARN gets a
	// clean 404 rather than being silently serviced by whichever service's
	// route happens to match, matching how real AWS would never route such
	// a request to either service in the first place.
	{
		tagRouters := map[string]http.Handler{}
		if cfg.Services["appsync"] {
			tagRouters["appsync"] = appsyncSvc.TagsRouter()
		}
		if cfg.Services["msk"] {
			tagRouters["kafka"] = mskSvc.TagsRouter()
		}
		if len(tagRouters) > 0 {
			r.Route("/v1/tags", func(sub chi.Router) {
				sub.HandleFunc("/*", tagsDispatch(tagRouters))
			})
		}
	}

	r.Get("/_health", newHealthHandler(cfg, store, enabledServiceNames, enabledTiers, enabledGoalTiers))

	// GET /_topology — full cross-region resource graph for the system map.
	r.Get("/_topology", newTopologyHandler(cfg, store))

	// Register POST / handler for AWS target and query-protocol dispatch.
	if len(dispatchers) > 0 || len(queryDispatchers) > 0 || len(disabledTargetPrefixes) > 0 {
		r.Post("/", targetDispatch(dispatchers, queryDispatchers, disabledTargetPrefixes))
	}

	r.NotFound(notFoundHandler)

	prof.mark("cross-service wiring + final routes")

	// Seal the startup phase timeline and record the ready timestamp atomically
	// so that startup_duration_ms and startup_phases share the same reference.
	prof.finalize()
	readyTime = time.Now()

	// Wire CloudFormation's internal dispatch router now that all routes are registered.
	if cfg.Services["cloudformation"] {
		cfnSvc.InitRouter(r)
	}

	// Start goroutine leak monitor in debug mode.
	// Samples every 5 s; dumps all goroutine stacks when the count stays above
	// 150 for 30 s so leaks can be diagnosed without reaching for a profiler.
	if cfg.Debug {
		mon := newGoroutineMonitor(logger)
		mon.Start()
		cleanups = append(cleanups, mon.Stop)
	}

	return r, preShutdown, func(ctx context.Context) {
			// Stop background service resources (e.g. Runtime API long-poll server).
			for _, st := range stoppers {
				t0 := time.Now()
				st.Stop(ctx)
				logger.Info("service stopped", zap.String("service", st.name), zap.Duration("elapsed", time.Since(t0)))
			}
			// Shut down the event bus worker pool after services have stopped
			// so in-flight events are drained before the workers exit.
			t0 := time.Now()
			bus.Stop()
			logger.Info("event bus stopped", zap.Duration("elapsed", time.Since(t0)))
			// Close other handles (e.g. SMTP listener).
			for _, fn := range cleanups {
				fn()
			}
		}, func() {
			// Wait for all background-initialising services to become ready.
			for _, rd := range readiers {
				rd.WaitReady()
			}
		}
}

// reconcileDockerContainers lists all managed containers for the given service
// label and calls ReconcileContainers on the service if it implements the
// ContainerReconciler interface. This is called once after Docker becomes
// available so services can sync their stored state against actual container state.
func reconcileDockerContainers(ctx context.Context, dc *docker.Client, service string, svc Service, logger *zap.Logger) {
	reconciler, ok := svc.(ContainerReconciler)
	if !ok {
		return
	}
	containers, err := dc.ListContainers(ctx, service)
	if err != nil {
		logger.Warn("docker reconcile: list containers failed", zap.String("service", service), zap.Error(err))
		return
	}
	logger.Info("docker reconcile: syncing container state", zap.String("service", service), zap.Int("containers", len(containers)))
	reconciler.ReconcileContainers(ctx, containers)
}

// reconcileDockerNetworks is the network parallel of reconcileDockerContainers.
// It lists all managed networks for the given service and calls ReconcileNetworks
// on the service if it implements NetworkReconciler.
func reconcileDockerNetworks(ctx context.Context, dc *docker.Client, service string, svc Service, logger *zap.Logger) {
	reconciler, ok := svc.(NetworkReconciler)
	if !ok {
		return
	}
	networks, err := dc.ListNetworks(ctx, service)
	if err != nil {
		logger.Warn("docker reconcile: list networks failed", zap.String("service", service), zap.Error(err))
		return
	}
	logger.Info("docker reconcile: syncing network state", zap.String("service", service), zap.Int("networks", len(networks)))
	reconciler.ReconcileNetworks(ctx, networks)
}

// targetDispatch returns a handler that inspects the X-Amz-Target header and
// delegates to the correct service dispatcher. SQS/DynamoDB use X-Amz-Target;
// SNS uses the Query protocol (form-encoded body with Action field + XML).
func targetDispatch(dispatchers []TargetDispatcher, queryDispatchers []QueryDispatcher, disabledPrefixes []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		for _, td := range dispatchers {
			if strings.HasPrefix(target, td.TargetPrefix()) {
				td.Dispatch(w, r)
				return
			}
		}
		// Check if the target matches a known-but-disabled service.
		if target != "" {
			for _, prefix := range disabledPrefixes {
				if strings.HasPrefix(target, prefix) {
					protocol.WriteJSONError(w, r, protocol.ErrServiceDisabled)
					return
				}
			}
		}
		// No X-Amz-Target match — try AWS Query protocol services (SNS, SES v1).
		// ParseForm caches results; safe to call before dispatching.
		if len(queryDispatchers) > 0 && strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			if err := r.ParseForm(); err == nil {
				action := r.FormValue("Action")
				version := r.FormValue("Version")
				// First pass: dispatchers that declare ownership by API version.
				// Version is a stricter discriminator than action name — it avoids
				// action name collisions between services (e.g. both SES and
				// CloudFormation implement "GetTemplate").
				for _, qd := range queryDispatchers {
					if ver, ok := qd.(QueryVersionOwner); ok {
						if ver.OwnsVersion(version) {
							qd.DispatchQuery(w, r)
							return
						}
						continue
					}
				}
				// Second pass: dispatchers that declare ownership by action name.
				for _, qd := range queryDispatchers {
					if owner, ok := qd.(QueryActionOwner); ok {
						if owner.OwnsAction(action) {
							qd.DispatchQuery(w, r)
							return
						}
						continue
					}
				}
				// Final fallback: first dispatcher with no ownership declaration.
				for _, qd := range queryDispatchers {
					if _, isActionOwner := qd.(QueryActionOwner); isActionOwner {
						continue
					}
					if _, isVersionOwner := qd.(QueryVersionOwner); isVersionOwner {
						continue
					}
					qd.DispatchQuery(w, r)
					return
				}
			}
		}
		// No match — return an error in the appropriate format.
		// Query-protocol requests (IAM, STS, etc.) expect XML; JSON-target
		// requests expect JSON. Sending JSON to an XML-expecting SDK causes
		// a parse error ("char '{' is not expected").
		if r.Body != nil {
			io.Copy(io.Discard, r.Body) //nolint:errcheck
			r.Body.Close()              //nolint:errcheck
		}
		if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			action := r.FormValue("Action")
			var escaped strings.Builder
			xml.EscapeText(&escaped, []byte(action)) //nolint:errcheck
			body := []byte(`<?xml version="1.0" encoding="UTF-8"?><ErrorResponse><Error><Code>NotImplemented</Code><Message>Unknown action: ` + escaped.String() + `</Message></Error></ErrorResponse>`)
			w.Header().Set("Content-Type", "text/xml")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusBadRequest)
			w.Write(body)
			return
		}
		body, _ := json.Marshal(struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
		}{Type: "UnknownOperationException", Message: "Unknown target: " + target})
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusBadRequest)
		w.Write(body)
	}
}

func smithyRPCDispatch(dispatchers map[string]TargetDispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Smithy-Protocol")), "rpc-v2-cbor") {
			notFoundHandler(w, r)
			return
		}
		service := strings.ToLower(chi.URLParam(r, "service"))
		if td, ok := dispatchers[service]; ok {
			td.Dispatch(w, r)
			return
		}
		w.Header().Set("x-emulator-unsupported-protocol", codec.NameRPCv2CBOR)
		codec.RPCv2CBOR.WriteError(w, r, &protocol.AWSError{
			Code:       "UnsupportedProtocol",
			Message:    "This service does not support wire protocol " + codec.NameRPCv2CBOR + ".",
			HTTPStatus: http.StatusUnsupportedMediaType,
		})
	}
}

// serviceDisabledHandler returns a 503 ServiceDisabled response when a service
// is known to the emulator but not enabled. The response format is determined
// by the Content-Type of the incoming request:
//   - application/x-www-form-urlencoded → AWS Query-protocol XML
//   - everything else                   → AWS JSON (Lambda, DynamoDB, etc.)
func serviceDisabledHandler(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		protocol.WriteQueryXMLError(w, r, protocol.ErrServiceDisabled)
		return
	}
	protocol.WriteJSONError(w, r, protocol.ErrServiceDisabled)
}

// queryGetMiddleware returns middleware that intercepts GET / requests carrying
// an AWS Query-protocol Action param (e.g. SNS UnsubscribeURL).
// It uses a pointer to the dispatchers slice so it reads the fully-populated
// slice at request time — the slice is filled in by the service registration
// loop after middleware registration.
func queryGetMiddleware(queryDispatchers *[]QueryDispatcher) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/" || r.URL.Query().Get("Action") == "" {
				next.ServeHTTP(w, r)
				return
			}
			if err := r.ParseForm(); err != nil {
				next.ServeHTTP(w, r)
				return
			}
			action := r.FormValue("Action")
			version := r.FormValue("Version")
			// First pass: version owners.
			for _, qd := range *queryDispatchers {
				if ver, ok := qd.(QueryVersionOwner); ok && ver.OwnsVersion(version) {
					qd.DispatchQuery(w, r)
					return
				}
			}
			// Second pass: action owners.
			for _, qd := range *queryDispatchers {
				if owner, ok := qd.(QueryActionOwner); ok && owner.OwnsAction(action) {
					qd.DispatchQuery(w, r)
					return
				}
			}
			// No dispatcher claimed this action — fall through to chi's normal routing.
			next.ServeHTTP(w, r)
		})
	}
}

// v2APIsDispatch returns a handler that dispatches /v2/apis requests to either
// API Gateway v2 or AppSync Events API based on the SigV4 credential scope
// service name. If the credential scope indicates "appsync", the request is
// routed to the AppSync Events API handler; otherwise it falls back to the
// API Gateway v2 handler (the more commonly used service at this path).
func v2APIsDispatch(apigwRouter, appsyncRouter http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc := middleware.ServiceFromCredential(r)
		if svc == "appsync" && appsyncRouter != nil {
			appsyncRouter.ServeHTTP(w, r)
			return
		}
		if apigwRouter != nil {
			apigwRouter.ServeHTTP(w, r)
			return
		}
		// Neither service enabled — 404.
		http.NotFound(w, r)
	}
}

// tagsDispatch returns a handler that dispatches /v1/tags/{resourceArn}
// requests to whichever service's tag router owns the resourceArn, as
// identified by protocol.ServiceFromARN. A resourceArn that doesn't parse,
// or whose service isn't one of the given routers, gets a 404 — no service
// silently claims a request it doesn't recognize.
func tagsDispatch(routers map[string]http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resourceArn := chi.URLParam(r, "*")
		// AWS SDKs URL-encode the ARN in the path (e.g. ":" as "%3A").
		if decoded, err := url.PathUnescape(resourceArn); err == nil {
			resourceArn = decoded
		}
		if router, ok := routers[protocol.ServiceFromARN(resourceArn)]; ok {
			router.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}
}
