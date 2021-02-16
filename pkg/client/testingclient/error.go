package testingclient

import (
	"context"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Action string

const (
	GetAction    Action = "get"
	CreateAction Action = "create"
	DeleteAction Action = "delete"
	UpdateAction Action = "update"
	PatchAction  Action = "patch"
	AnyAction    Action = "*"
)

type resourceActionKey struct {
	action    Action
	kind      schema.GroupVersionKind
	objectKey client.ObjectKey
}

var (
	AnyKind    = &unstructured.Unstructured{}
	AnyObject  = client.ObjectKey{}
	anyKindGVK = schema.GroupVersionKind{Kind: "*"}
)

type ErrorInjector struct {
	delegate       client.Client
	errorsToReturn map[resourceActionKey]error
}

var _ client.Client = ErrorInjector{}

func NewErrorInjector(cl client.Client) *ErrorInjector {
	injectedErrors := make(map[resourceActionKey]error)

	return &ErrorInjector{
		delegate:       cl,
		errorsToReturn: injectedErrors,
	}
}

func (c ErrorInjector) Scheme() *runtime.Scheme {
	return c.delegate.Scheme()
}

func (c ErrorInjector) RESTMapper() meta.RESTMapper {
	return c.delegate.RESTMapper()
}

func (c ErrorInjector) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	err := c.getStubbedError(GetAction, obj, key)
	if err != nil {
		return err
	}

	return c.delegate.Get(ctx, key, obj)
}

func (c ErrorInjector) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	panic("implement me")
}

func (c ErrorInjector) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	err := c.getStubbedError(CreateAction, obj, client.ObjectKeyFromObject(obj))
	if err != nil {
		return err
	}

	return c.delegate.Create(ctx, obj, opts...)
}

func (c ErrorInjector) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	err := c.getStubbedError(DeleteAction, obj, client.ObjectKeyFromObject(obj))
	if err != nil {
		return err
	}

	return c.delegate.Delete(ctx, obj, opts...)
}

func (c ErrorInjector) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	err := c.getStubbedError(UpdateAction, obj, client.ObjectKeyFromObject(obj))
	if err != nil {
		return err
	}

	return c.delegate.Update(ctx, obj, opts...)
}

func (c ErrorInjector) Patch(ctx context.Context,
	obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	err := c.getStubbedError(PatchAction, obj, client.ObjectKeyFromObject(obj))
	if err != nil {
		return err
	}

	return c.delegate.Patch(ctx, obj, patch, opts...)
}

func (c ErrorInjector) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	panic("implement me")
}

func (c ErrorInjector) Status() client.StatusWriter {
	panic("implement me")
}

func (c ErrorInjector) getStubbedError(action Action, kind client.Object, objectKey client.ObjectKey) error {
	gvk := mustGVKForObject(kind, c.Scheme())

	for _, k := range []resourceActionKey{
		{action, gvk, objectKey},           // (1) 0 wildcards
		{action, gvk, AnyObject},           // (2) 1 wildcard
		{AnyAction, gvk, objectKey},        // (3) 1 wildcard
		{action, anyKindGVK, objectKey},    // (4) 1 wildcard
		{AnyAction, gvk, AnyObject},        // (5) 2 wildcards
		{action, anyKindGVK, AnyObject},    // (6) 2 wildcards
		{AnyAction, anyKindGVK, objectKey}, // (7) 2 wildcards
		{AnyAction, anyKindGVK, AnyObject}, // (8) 3 wildcards
	} {
		if err, ok := c.errorsToReturn[k]; ok {
			return err
		}
	}
	return nil
}

func (a Action) IsValid() bool {
	switch a {
	case GetAction, CreateAction, DeleteAction, UpdateAction, PatchAction, AnyAction:
		return true
	}
	return false
}

// InjectError will cause ErrorInjector to return an error for the given (action, kind, objectKey) tuple.
// Wildcards are supported for each part of the tuple:
// Pass objectKey = AnyObject to match any object identity.
// Pass kind = AnyKind to match any type of object.
// Pass action = AnyAction to match any client action.
func (c *ErrorInjector) InjectError(action Action, kind client.Object, objectKey client.ObjectKey, injectedError error) {
	gvk := anyKindGVK
	if kind != AnyKind {
		gvk = mustGVKForObject(kind, c.Scheme())
	}
	if action.IsValid() {
		c.errorsToReturn[resourceActionKey{action, gvk, objectKey}] = injectedError
	}
}
