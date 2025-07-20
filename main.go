/*
Copyright 2020 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	flag "github.com/spf13/pflag"
	"helm.sh/helm/v3/pkg/getter"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcfg "sigs.k8s.io/controller-runtime/pkg/config"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/fluxcd/pkg/auth"
	pkgcache "github.com/fluxcd/pkg/cache"
	"github.com/fluxcd/pkg/git"
	"github.com/fluxcd/pkg/runtime/client"
	helper "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/runtime/events"
	feathelper "github.com/fluxcd/pkg/runtime/features"
	"github.com/fluxcd/pkg/runtime/jitter"
	"github.com/fluxcd/pkg/runtime/leaderelection"
	"github.com/fluxcd/pkg/runtime/logger"
	"github.com/fluxcd/pkg/runtime/metrics"
	"github.com/fluxcd/pkg/runtime/pprof"
	"github.com/fluxcd/pkg/runtime/probes"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	// +kubebuilder:scaffold:imports

	"github.com/fluxcd/source-controller/internal/cache"
	"github.com/fluxcd/source-controller/internal/controller"
	intdigest "github.com/fluxcd/source-controller/internal/digest"
	"github.com/fluxcd/source-controller/internal/features"
	"github.com/fluxcd/source-controller/internal/helm"
	"github.com/fluxcd/source-controller/internal/helm/registry"
	"github.com/fluxcd/source-controller/pkg/storage"
)

const controllerName = "source-controller"

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	getters  = getter.Providers{
		getter.Provider{
			Schemes: []string{"http", "https"},
			New:     getter.NewHTTPGetter,
		},
		getter.Provider{
			Schemes: []string{"oci"},
			New:     getter.NewOCIGetter,
		},
	}
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(sourcev1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	const (
		tokenCacheDefaultMaxSize = 100
	)

	var (
		metricsAddr              string
		eventsAddr               string
		healthAddr               string
		storagePath              string
		storageAddr              string
		storageAdvAddr           string
		concurrent               int
		requeueDependency        time.Duration
		helmIndexLimit           int64
		helmChartLimit           int64
		helmChartFileLimit       int64
		clientOptions            client.Options
		logOptions               logger.Options
		leaderElectionOptions    leaderelection.Options
		rateLimiterOptions       helper.RateLimiterOptions
		featureGates             feathelper.FeatureGates
		watchOptions             helper.WatchOptions
		intervalJitterOptions    jitter.IntervalOptions
		helmCacheMaxSize         int
		helmCacheTTL             string
		helmCachePurgeInterval   string
		artifactRetentionTTL     time.Duration
		artifactRetentionRecords int
		artifactDigestAlgo       string
		tokenCacheOptions        pkgcache.TokenFlags
		// Storage backend configuration
		storageBackend           string
		s3Bucket                 string
		s3Prefix                 string
		s3Region                 string
		s3Endpoint               string
		s3ForcePathStyle         bool
	)

	flag.StringVar(&metricsAddr, "metrics-addr", envOrDefault("METRICS_ADDR", ":8080"),
		"The address the metric endpoint binds to.")
	flag.StringVar(&eventsAddr, "events-addr", envOrDefault("EVENTS_ADDR", ""),
		"The address of the events receiver.")
	flag.StringVar(&healthAddr, "health-addr", ":9440", "The address the health endpoint binds to.")
	flag.StringVar(&storagePath, "storage-path", envOrDefault("STORAGE_PATH", ""),
		"The local storage path.")
	flag.StringVar(&storageAddr, "storage-addr", envOrDefault("STORAGE_ADDR", ":9090"),
		"The address the static file server binds to.")
	flag.StringVar(&storageAdvAddr, "storage-adv-addr", envOrDefault("STORAGE_ADV_ADDR", ""),
		"The advertised address of the static file server.")
	flag.StringVar(&storageBackend, "storage-backend", envOrDefault("STORAGE_BACKEND", "filesystem"),
		"The storage backend type. Options: filesystem, s3")
	flag.StringVar(&s3Bucket, "s3-bucket", envOrDefault("S3_BUCKET", ""),
		"The S3 bucket name (required when storage-backend=s3).")
	flag.StringVar(&s3Prefix, "s3-prefix", envOrDefault("S3_PREFIX", ""),
		"The S3 key prefix for artifacts.")
	flag.StringVar(&s3Region, "s3-region", envOrDefault("S3_REGION", "us-east-1"),
		"The S3 region.")
	flag.StringVar(&s3Endpoint, "s3-endpoint", envOrDefault("S3_ENDPOINT", ""),
		"The S3 custom endpoint (for MinIO, etc).")
	flag.BoolVar(&s3ForcePathStyle, "s3-force-path-style", false,
		"Force S3 path-style URLs (required for MinIO).")
	flag.IntVar(&concurrent, "concurrent", 2, "The number of concurrent reconciles per controller.")
	flag.Int64Var(&helmIndexLimit, "helm-index-max-size", helm.MaxIndexSize,
		"The max allowed size in bytes of a Helm repository index file.")
	flag.Int64Var(&helmChartLimit, "helm-chart-max-size", helm.MaxChartSize,
		"The max allowed size in bytes of a Helm chart file.")
	flag.Int64Var(&helmChartFileLimit, "helm-chart-file-max-size", helm.MaxChartFileSize,
		"The max allowed size in bytes of a file in a Helm chart.")
	flag.DurationVar(&requeueDependency, "requeue-dependency", 30*time.Second,
		"The interval at which failing dependencies are reevaluated.")
	flag.IntVar(&helmCacheMaxSize, "helm-cache-max-size", 0,
		"The maximum size of the cache in number of indexes.")
	flag.StringVar(&helmCacheTTL, "helm-cache-ttl", "15m",
		"The TTL of an index in the cache. Valid time units are ns, us (or µs), ms, s, m, h.")
	flag.StringVar(&helmCachePurgeInterval, "helm-cache-purge-interval", "1m",
		"The interval at which the cache is purged. Valid time units are ns, us (or µs), ms, s, m, h.")
	flag.StringSliceVar(&git.KexAlgos, "ssh-kex-algos", []string{},
		"The list of key exchange algorithms to use for ssh connections, arranged from most preferred to the least.")
	flag.StringSliceVar(&git.HostKeyAlgos, "ssh-hostkey-algos", []string{},
		"The list of hostkey algorithms to use for ssh connections, arranged from most preferred to the least.")
	flag.DurationVar(&artifactRetentionTTL, "artifact-retention-ttl", 60*time.Second,
		"The duration of time that artifacts from previous reconciliations will be kept in storage before being garbage collected.")
	flag.IntVar(&artifactRetentionRecords, "artifact-retention-records", 2,
		"The maximum number of artifacts to be kept in storage after a garbage collection.")
	flag.StringVar(&artifactDigestAlgo, "artifact-digest-algo", intdigest.Canonical.String(),
		"The algorithm to use to calculate the digest of artifacts.")

	clientOptions.BindFlags(flag.CommandLine)
	logOptions.BindFlags(flag.CommandLine)
	leaderElectionOptions.BindFlags(flag.CommandLine)
	rateLimiterOptions.BindFlags(flag.CommandLine)
	featureGates.BindFlags(flag.CommandLine)
	watchOptions.BindFlags(flag.CommandLine)
	intervalJitterOptions.BindFlags(flag.CommandLine)
	tokenCacheOptions.BindFlags(flag.CommandLine, tokenCacheDefaultMaxSize)

	flag.Parse()

	logger.SetLogger(logger.NewLogger(logOptions))

	if err := featureGates.WithLogger(setupLog).SupportedFeatures(features.FeatureGates()); err != nil {
		setupLog.Error(err, "unable to load feature gates")
		os.Exit(1)
	}

	switch enabled, err := features.Enabled(auth.FeatureGateObjectLevelWorkloadIdentity); {
	case err != nil:
		setupLog.Error(err, "unable to check feature gate "+auth.FeatureGateObjectLevelWorkloadIdentity)
		os.Exit(1)
	case enabled:
		auth.EnableObjectLevelWorkloadIdentity()
	}

	if err := intervalJitterOptions.SetGlobalJitter(nil); err != nil {
		setupLog.Error(err, "unable to set global jitter")
		os.Exit(1)
	}

	mgr := mustSetupManager(metricsAddr, healthAddr, concurrent, watchOptions, clientOptions, leaderElectionOptions)

	probes.SetupChecks(mgr, setupLog)

	metrics := helper.NewMetrics(mgr, metrics.MustMakeRecorder(), sourcev1.SourceFinalizer)
	cacheRecorder := cache.MustMakeMetrics()
	eventRecorder := mustSetupEventRecorder(mgr, eventsAddr, controllerName)
	
	ctx := ctrl.SetupSignalHandler()
	storageProvider := mustInitStorage(ctx, storagePath, storageAdvAddr, artifactRetentionTTL, artifactRetentionRecords, artifactDigestAlgo, storageBackend, s3Bucket, s3Prefix, s3Region, s3Endpoint, s3ForcePathStyle)
	
	// Create legacy storage adapter for backwards compatibility
	legacyStorage := storage.NewLegacyStorageAdapter(storageProvider, storagePath, storageAdvAddr)

	mustSetupHelmLimits(helmIndexLimit, helmChartLimit, helmChartFileLimit)
	helmIndexCache, helmIndexCacheItemTTL := mustInitHelmCache(helmCacheMaxSize, helmCacheTTL, helmCachePurgeInterval)

	var tokenCache *pkgcache.TokenCache
	if tokenCacheOptions.MaxSize > 0 {
		var err error
		tokenCache, err = pkgcache.NewTokenCache(tokenCacheOptions.MaxSize,
			pkgcache.WithMaxDuration(tokenCacheOptions.MaxDuration),
			pkgcache.WithMetricsRegisterer(ctrlmetrics.Registry),
			pkgcache.WithMetricsPrefix("gotk_token_"))
		if err != nil {
			setupLog.Error(err, "unable to create token cache")
			os.Exit(1)
		}
	}

	if err := (&controller.GitRepositoryReconciler{
		Client:         mgr.GetClient(),
		EventRecorder:  eventRecorder,
		Metrics:        metrics,
		Storage:        legacyStorage,
		ControllerName: controllerName,
		TokenCache:     tokenCache,
	}).SetupWithManagerAndOptions(mgr, controller.GitRepositoryReconcilerOptions{
		DependencyRequeueInterval: requeueDependency,
		RateLimiter:               helper.GetRateLimiter(rateLimiterOptions),
	}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", sourcev1.GitRepositoryKind)
		os.Exit(1)
	}

	if err := (&controller.HelmRepositoryReconciler{
		Client:         mgr.GetClient(),
		EventRecorder:  eventRecorder,
		Metrics:        metrics,
		Storage:        legacyStorage,
		Getters:        getters,
		ControllerName: controllerName,
		Cache:          helmIndexCache,
		TTL:            helmIndexCacheItemTTL,
		CacheRecorder:  cacheRecorder,
	}).SetupWithManagerAndOptions(mgr, controller.HelmRepositoryReconcilerOptions{
		RateLimiter: helper.GetRateLimiter(rateLimiterOptions),
	}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", sourcev1.HelmRepositoryKind)
		os.Exit(1)
	}

	if err := (&controller.HelmChartReconciler{
		Client:                  mgr.GetClient(),
		RegistryClientGenerator: registry.ClientGenerator,
		Storage:                 legacyStorage,
		Getters:                 getters,
		EventRecorder:           eventRecorder,
		Metrics:                 metrics,
		ControllerName:          controllerName,
		Cache:                   helmIndexCache,
		TTL:                     helmIndexCacheItemTTL,
		CacheRecorder:           cacheRecorder,
	}).SetupWithManagerAndOptions(ctx, mgr, controller.HelmChartReconcilerOptions{
		RateLimiter: helper.GetRateLimiter(rateLimiterOptions),
	}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", sourcev1.HelmChartKind)
		os.Exit(1)
	}

	if err := (&controller.BucketReconciler{
		Client:         mgr.GetClient(),
		EventRecorder:  eventRecorder,
		Metrics:        metrics,
		Storage:        legacyStorage,
		ControllerName: controllerName,
	}).SetupWithManagerAndOptions(mgr, controller.BucketReconcilerOptions{
		RateLimiter: helper.GetRateLimiter(rateLimiterOptions),
	}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", sourcev1.BucketKind)
		os.Exit(1)
	}

	if err := (&controller.OCIRepositoryReconciler{
		Client:         mgr.GetClient(),
		Storage:        legacyStorage,
		EventRecorder:  eventRecorder,
		ControllerName: controllerName,
		TokenCache:     tokenCache,
		Metrics:        metrics,
	}).SetupWithManagerAndOptions(mgr, controller.OCIRepositoryReconcilerOptions{
		RateLimiter: helper.GetRateLimiter(rateLimiterOptions),
	}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", sourcev1.OCIRepositoryKind)
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// Start the distributed artifact server
	// This can run on all pods, not just the leader!
	go func() {
		artifactServer := storage.NewArtifactServer(ctx, storageProvider, setupLog.WithName("artifact-server"))
		if err := artifactServer.ListenAndServe(storageAddr); err != nil && err != http.ErrServerClosed {
			setupLog.Error(err, "artifact server error")
			os.Exit(1)
		}
	}()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func startFileServer(path string, address string) {
	setupLog.Info("starting file server")
	fs := http.FileServer(http.Dir(path))
	mux := http.NewServeMux()
	mux.Handle("/", fs)
	err := http.ListenAndServe(address, mux)
	if err != nil {
		setupLog.Error(err, "file server error")
	}
}

func mustSetupEventRecorder(mgr ctrl.Manager, eventsAddr, controllerName string) record.EventRecorder {
	eventRecorder, err := events.NewRecorder(mgr, ctrl.Log, eventsAddr, controllerName)
	if err != nil {
		setupLog.Error(err, "unable to create event recorder")
		os.Exit(1)
	}
	return eventRecorder
}

func mustSetupManager(metricsAddr, healthAddr string, maxConcurrent int,
	watchOpts helper.WatchOptions, clientOpts client.Options, leaderOpts leaderelection.Options) ctrl.Manager {

	watchNamespace := ""
	if !watchOpts.AllNamespaces {
		watchNamespace = os.Getenv("RUNTIME_NAMESPACE")
	}

	watchSelector, err := helper.GetWatchSelector(watchOpts)
	if err != nil {
		setupLog.Error(err, "unable to configure watch label selector for manager")
		os.Exit(1)
	}

	var disableCacheFor []ctrlclient.Object
	shouldCache, err := features.Enabled(features.CacheSecretsAndConfigMaps)
	if err != nil {
		setupLog.Error(err, "unable to check feature gate "+features.CacheSecretsAndConfigMaps)
		os.Exit(1)
	}
	if !shouldCache {
		disableCacheFor = append(disableCacheFor, &corev1.Secret{}, &corev1.ConfigMap{})
	}

	leaderElectionId := fmt.Sprintf("%s-%s", controllerName, "leader-election")
	if watchOpts.LabelSelector != "" {
		leaderElectionId = leaderelection.GenerateID(leaderElectionId, watchOpts.LabelSelector)
	}

	restConfig := client.GetConfigOrDie(clientOpts)
	mgrConfig := ctrl.Options{
		Scheme:                        scheme,
		HealthProbeBindAddress:        healthAddr,
		LeaderElection:                leaderOpts.Enable,
		LeaderElectionReleaseOnCancel: leaderOpts.ReleaseOnCancel,
		LeaseDuration:                 &leaderOpts.LeaseDuration,
		RenewDeadline:                 &leaderOpts.RenewDeadline,
		RetryPeriod:                   &leaderOpts.RetryPeriod,
		LeaderElectionID:              leaderElectionId,
		Logger:                        ctrl.Log,
		Client: ctrlclient.Options{
			Cache: &ctrlclient.CacheOptions{
				DisableFor: disableCacheFor,
			},
		},
		Cache: ctrlcache.Options{
			ByObject: map[ctrlclient.Object]ctrlcache.ByObject{
				&sourcev1.GitRepository{}:  {Label: watchSelector},
				&sourcev1.HelmRepository{}: {Label: watchSelector},
				&sourcev1.HelmChart{}:      {Label: watchSelector},
				&sourcev1.Bucket{}:         {Label: watchSelector},
				&sourcev1.OCIRepository{}:  {Label: watchSelector},
			},
		},
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			ExtraHandlers: pprof.GetHandlers(),
		},
		Controller: ctrlcfg.Controller{
			RecoverPanic:            ptr.To(true),
			MaxConcurrentReconciles: maxConcurrent,
		},
	}

	if watchNamespace != "" {
		mgrConfig.Cache.DefaultNamespaces = map[string]ctrlcache.Config{
			watchNamespace: ctrlcache.Config{},
		}
	}

	mgr, err := ctrl.NewManager(restConfig, mgrConfig)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	return mgr
}

func mustSetupHelmLimits(indexLimit, chartLimit, chartFileLimit int64) {
	helm.MaxIndexSize = indexLimit
	helm.MaxChartSize = chartLimit
	helm.MaxChartFileSize = chartFileLimit
}

func mustInitHelmCache(maxSize int, itemTTL, purgeInterval string) (*cache.Cache, time.Duration) {
	if maxSize <= 0 {
		setupLog.Info("caching of Helm index files is disabled")
		return nil, -1
	}

	interval, err := time.ParseDuration(purgeInterval)
	if err != nil {
		setupLog.Error(err, "unable to parse Helm index cache purge interval")
		os.Exit(1)
	}

	ttl, err := time.ParseDuration(itemTTL)
	if err != nil {
		setupLog.Error(err, "unable to parse Helm index cache item TTL")
		os.Exit(1)
	}

	return cache.New(maxSize, interval), ttl
}

func mustInitStorage(ctx context.Context, path string, storageAdvAddr string, artifactRetentionTTL time.Duration, artifactRetentionRecords int, artifactDigestAlgo string, backend string, s3Bucket string, s3Prefix string, s3Region string, s3Endpoint string, s3ForcePathStyle bool) storage.StorageProvider {
	// storageAdvAddr is already set properly by the caller
	// No need to call determineAdvStorageAddr here

	if artifactDigestAlgo != intdigest.Canonical.String() {
		algo, err := intdigest.AlgorithmForName(artifactDigestAlgo)
		if err != nil {
			setupLog.Error(err, "unable to configure canonical digest algorithm")
			os.Exit(1)
		}
		intdigest.Canonical = algo
	}

	// Create storage configuration
	cfg := storage.Config{
		Backend:          storage.BackendType(backend),
		Hostname:         storageAdvAddr,
		RetentionTTL:     artifactRetentionTTL,
		RetentionRecords: artifactRetentionRecords,
		// Filesystem config
		FilesystemPath:   path,
		// S3 config
		S3Bucket:         s3Bucket,
		S3Prefix:         s3Prefix,
		S3Region:         s3Region,
		S3Endpoint:       s3Endpoint,
		S3ForcePathStyle: s3ForcePathStyle,
		S3URLExpiration:  15 * time.Minute,
	}

	provider, err := storage.NewProvider(ctx, cfg)
	if err != nil {
		setupLog.Error(err, "unable to initialise storage provider")
		os.Exit(1)
	}
	return provider
}

func determineAdvStorageAddr(storageAddr string) string {
	host, port, err := net.SplitHostPort(storageAddr)
	if err != nil {
		setupLog.Error(err, "unable to parse storage address")
		os.Exit(1)
	}
	switch host {
	case "":
		host = "localhost"
	case "0.0.0.0":
		host = os.Getenv("HOSTNAME")
		if host == "" {
			hn, err := os.Hostname()
			if err != nil {
				setupLog.Error(err, "0.0.0.0 specified in storage addr but hostname is invalid")
				os.Exit(1)
			}
			host = hn
		}
	}
	return net.JoinHostPort(host, port)
}

func envOrDefault(envName, defaultValue string) string {
	ret := os.Getenv(envName)
	if ret != "" {
		return ret
	}

	return defaultValue
}
