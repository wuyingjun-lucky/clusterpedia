package kubeapiserver

import (
	"fmt"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/endpoints/handlers"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	genericrequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/warning"
	"k8s.io/klog/v2"

	clusterv1alpha2 "github.com/clusterpedia-io/api/cluster/v1alpha2"

	clusterlister "github.com/clusterpedia-io/clusterpedia/pkg/generated/listers/cluster/v1alpha2"
	"github.com/clusterpedia-io/clusterpedia/pkg/kubeapiserver/discovery"
	"github.com/clusterpedia-io/clusterpedia/pkg/utils/request"
)

type ResourceHandler struct {
	minRequestTimeout time.Duration
	delegate          http.Handler

	rest          *RESTManager
	discovery     *discovery.DiscoveryManager
	clusterLister clusterlister.PediaClusterLister
}

func (r *ResourceHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	requestInfo, ok := genericrequest.RequestInfoFrom(req.Context())
	if !ok {
		responsewriters.ErrorNegotiated(
			apierrors.NewInternalError(fmt.Errorf("no RequestInfo found in the context")),
			Codecs, schema.GroupVersion{}, w, req,
		)
		return
	}

	// handle discovery request
	if !requestInfo.IsResourceRequest {
		r.discovery.ServeHTTP(w, req)
		return
	}

	gvr := schema.GroupVersionResource{Group: requestInfo.APIGroup, Version: requestInfo.APIVersion, Resource: requestInfo.Resource}

	clusterName := request.ClusterNameValue(req.Context())
	if !r.discovery.ResourceEnabled(clusterName, gvr) {
		r.delegate.ServeHTTP(w, req)
		return
	}

	info := r.rest.GetRESTResourceInfo(gvr)
	if info.Empty() {
		err := fmt.Errorf("not found request scope or resource storage")
		klog.ErrorS(err, "Failed to handle resource request", "resource", gvr)
		responsewriters.ErrorNegotiated(
			apierrors.NewInternalError(err),
			Codecs, gvr.GroupVersion(), w, req,
		)
		return
	}

	resource, reqScope, storage := info.APIResource, info.RequestScope, info.Storage
	if requestInfo.Namespace != "" && !resource.Namespaced {
		r.delegate.ServeHTTP(w, req)
		return
	}

	// Check the health of the cluster
	if clusterName != "" {
		cluster, err := r.clusterLister.Get(clusterName)
		if err != nil {
			err := fmt.Errorf("not found request cluster")
			klog.ErrorS(err, "Failed to handle resource request, not get cluster from cache", "cluster", clusterName, "resource", gvr)
			responsewriters.ErrorNegotiated(
				apierrors.NewInternalError(err),
				Codecs, gvr.GroupVersion(), w, req,
			)
			return
		}

		var msg string
		readyCondition := meta.FindStatusCondition(cluster.Status.Conditions, clusterv1alpha2.ClusterReadyCondition)
		switch {
		case readyCondition == nil:
			msg = fmt.Sprintf("%s is not ready and the resources obtained may be inaccurate.", clusterName)
		case readyCondition.Status != metav1.ConditionTrue:
			msg = fmt.Sprintf("%s is not ready and the resources obtained may be inaccurate, reason: %s", clusterName, readyCondition.Reason)
		}
		/*
			TODO(scyda): Determine the synchronization status of a specific resource

			for _, resource := range c.Status.Resources {
			}
		*/

		if msg != "" {
			warning.AddWarning(req.Context(), "", msg)
		}
	}

	var handler http.Handler
	switch requestInfo.Verb {
	case "get":
		if clusterName == "" {
			responsewriters.ErrorNegotiated(
				apierrors.NewBadRequest("please specify the cluster name when using the resource name to get a specific resource."),
				Codecs, gvr.GroupVersion(), w, req,
			)
			return
		}

		handler = handlers.GetResource(storage, reqScope)
	case "list":
		handler = handlers.ListResource(storage, nil, reqScope, false, r.minRequestTimeout)
	default:
		responsewriters.ErrorNegotiated(
			apierrors.NewMethodNotSupported(gvr.GroupResource(), requestInfo.Verb),
			Codecs, gvr.GroupVersion(), w, req,
		)
	}

	if handler != nil {
		handler.ServeHTTP(w, req)
	}
}
