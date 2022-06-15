package resourcerest

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericrequest "k8s.io/apiserver/pkg/endpoints/request"
	genericfeatures "k8s.io/apiserver/pkg/features"
	"k8s.io/apiserver/pkg/registry/rest"
	storeerr "k8s.io/apiserver/pkg/storage/errors"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	internal "github.com/clusterpedia-io/api/clusterpedia"
	"github.com/clusterpedia-io/api/clusterpedia/scheme"
	"github.com/clusterpedia-io/api/clusterpedia/v1beta1"

	"github.com/clusterpedia-io/clusterpedia/pkg/kubeapiserver/printers"
	"github.com/clusterpedia-io/clusterpedia/pkg/storage"
	"github.com/clusterpedia-io/clusterpedia/pkg/utils/request"
)

type RESTStorage struct {
	DefaultQualifiedResource schema.GroupResource

	NewFunc     func() runtime.Object
	NewListFunc func() runtime.Object

	Storage        storage.ResourceStorage
	TableConvertor rest.TableConvertor
}

var _ rest.Lister = &RESTStorage{}
var _ rest.Getter = &RESTStorage{}

func (s *RESTStorage) New() runtime.Object {
	return s.NewFunc()
}

func (s *RESTStorage) NewList() runtime.Object {
	return s.NewListFunc()
}

func (s *RESTStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	clusterName := request.ClusterNameValue(ctx)
	if clusterName == "" {
		return nil, errors.New("missing cluster")
	}

	requestInfo, ok := genericrequest.RequestInfoFrom(ctx)
	if !ok {
		return nil, errors.New("missing RequestInfo")
	}

	obj := s.New()
	if err := s.Storage.Get(ctx, clusterName, requestInfo.Namespace, name, obj); err != nil {
		return nil, storeerr.InterpretGetError(err, s.DefaultQualifiedResource, name)
	}
	return obj, nil
}

func (s *RESTStorage) List(ctx context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	var opts internal.ListOptions
	query := request.RequestQueryFrom(ctx)
	if err := scheme.ParameterCodec.DecodeParameters(query, v1beta1.SchemeGroupVersion, &opts); err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}
	return s.list(ctx, &opts)
}

func (s *RESTStorage) list(ctx context.Context, options *internal.ListOptions) (runtime.Object, error) {
	requestInfo, ok := genericrequest.RequestInfoFrom(ctx)
	if !ok {
		return nil, errors.New("missing RequestInfo")
	}

	if requestInfo.Namespace != "" {
		options.Namespaces = []string{requestInfo.Namespace}
	}

	if cluster := request.ClusterNameValue(ctx); cluster != "" {
		options.ClusterNames = []string{cluster}
	}

	if (options.OwnerUID != "" || options.OwnerName != "") && len(options.ClusterNames) != 1 {
		return nil, apierrors.NewBadRequest("If searching by owner uid or name, then the cluster must be specified")
	}

	if options.WithRemainingCount == nil {
		if enabled := utilfeature.DefaultFeatureGate.Enabled(genericfeatures.RemainingItemCount); enabled {
			options.WithRemainingCount = &enabled
		}
	}

	objs := s.NewList()
	if err := s.Storage.List(ctx, objs, options); err != nil {
		return nil, storeerr.InterpretListError(err, s.DefaultQualifiedResource)
	}

	return objs, nil
}

func (s *RESTStorage) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	if s.TableConvertor != nil {
		return s.TableConvertor.ConvertToTable(ctx, object, tableOptions)
	}

	return printers.NewDefaultTableConvertor(s.DefaultQualifiedResource).ConvertToTable(ctx, object, tableOptions)
}
