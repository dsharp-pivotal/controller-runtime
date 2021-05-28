package testingclient

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type Reactive struct {
	Fake testing.Fake
	delegate client.Client
}

var _ client.Client = &Reactive{}

func (r *Reactive) Scheme() *runtime.Scheme {
	return r.delegate.Scheme()
}

func (r *Reactive) RESTMapper() meta.RESTMapper {
	return r.delegate.RESTMapper()
}

// Workaround for testing.ListAction missing GetKind(). It looks like an oversight.
type workaroundListAction interface {
	testing.ListAction
	GetKind() schema.GroupVersionKind
}

func NewReactiveClient(delegate client.Client) *Reactive {
	r := &Reactive{
		delegate: delegate,
	}

	r.Fake.PrependReactor("*", "*", func(action testing.Action) (bool, runtime.Object, error) {
		ctx := context.TODO()
		switch Verb(action.GetVerb()) {
		case GetVerb:
			a := action.(testing.GetAction)
			key := types.NamespacedName{
				Name:      a.GetName(),
				Namespace: a.GetNamespace(),
			}
			obj := r.newNamedObject(r.kindForResource(a.GetResource()), a.GetNamespace(), a.GetName())
			err := r.delegate.Get(ctx, key, obj)
			return true, obj, err
		case CreateVerb:
			a := action.(testing.CreateAction)
			err := r.delegate.Create(ctx, a.GetObject().(client.Object))
			return true, nil, err
		case DeleteVerb:
			a := action.(testing.DeleteAction)
			obj := r.newNamedObject(r.kindForResource(a.GetResource()), a.GetNamespace(), a.GetName())
			err := r.delegate.Delete(ctx, obj)
			return true, nil, err
		case UpdateVerb:
			a := action.(testing.UpdateAction)
			err := r.delegate.Update(ctx, a.GetObject().(client.Object))
			return true, nil, err
		case PatchVerb:
			a := action.(testing.PatchAction)
			obj := r.newNamedObject(r.kindForResource(a.GetResource()), a.GetNamespace(), a.GetName())
			patch := client.RawPatch(a.GetPatchType(), a.GetPatch())
			err := r.delegate.Patch(ctx, obj, patch)
			return true, nil, err
		case ListVerb:
			a := action.(workaroundListAction)
			obj := r.newObjectList(a.GetKind())
			err := r.delegate.List(ctx, obj,
				client.MatchingFieldsSelector{Selector: a.GetListRestrictions().Fields},
				client.MatchingLabelsSelector{Selector: a.GetListRestrictions().Labels},
				client.InNamespace(a.GetNamespace()),
			)
			return true, obj, err
		default:
			return true, nil, fmt.Errorf("unsupported action for verb %#v", action.GetVerb())
		}
	})

	return r
}

func (r *Reactive) gvrForObject(obj runtime.Object) schema.GroupVersionResource {
	kinds, _, err := r.Scheme().ObjectKinds(obj)
	if err != nil {
		panic(fmt.Errorf("getting ObjectKinds: %w", err))
	}
	if len(kinds) != 1 {
		panic(errors.New("expected exactly one Kind for obj"))
	}
	gvk := kinds[0]

	rm, err := r.RESTMapper().RESTMapping(gvk.GroupKind())
	if err != nil {
		panic(fmt.Errorf("getting REST mapping for %s: %w", gvk.GroupKind(), err))
	}
	gvr := rm.Resource

	return gvr
}

func (r *Reactive) kindForResource(resource schema.GroupVersionResource) schema.GroupVersionKind {
	kind, err := r.RESTMapper().KindFor(resource)
	if err != nil {
		panic(fmt.Errorf("getting Kind for resource %s: %w", resource, err))
	}
	return kind
}

func (r *Reactive) newNamedObject(kind schema.GroupVersionKind, namespace, name string) client.Object {
	rObj := r.newRuntimeObject(kind)
	cObj, ok := rObj.(client.Object)
	if !ok {
		panic("expected object to implement client.Object. Does it implement metav1.Object?")
	}
	cObj.SetNamespace(namespace)
	cObj.SetName(name)
	return cObj
}

func (r *Reactive) newObjectList(kind schema.GroupVersionKind) client.ObjectList {
	rObj := r.newRuntimeObject(kind)
	cObj, ok := rObj.(client.ObjectList)
	if !ok {
		panic("expected object to implement client.ObjectList. Does it implement metav1.ListInterface?")
	}
	return cObj
}

func (r *Reactive) newRuntimeObject(kind schema.GroupVersionKind) runtime.Object {
	rObj, err := r.Scheme().New(kind)
	if err != nil {
		panic(fmt.Errorf("could not create a new %s (Is it registered with the Scheme?)", kind))
	}
	return rObj
}

// PrependReactor adds a reactor to the beginning of the chain.
func (r *Reactive) PrependReactor(verb Verb, kind client.Object, reaction testing.ReactionFunc) {
	resource := "*"
	if kind != AnyKind {
		gvr := r.gvrForObject(kind)
		resource = gvr.Resource
	}
	r.Fake.PrependReactor(string(verb), resource, reaction)
}

// Actions returns a chronologically ordered slice of actions called on the fake client.
func (r *Reactive) Actions() []testing.Action {
	return r.Fake.Actions()
}

// ClearActions clears the history of actions called on the fake client.
func (r *Reactive) ClearActions() {
	r.Fake.ClearActions()
}

func (r *Reactive) populateGVK(obj runtime.Object) {
	// Set GVK using reflection. Normally the apiserver would populate this, but we need it earlier.
	gvk, err := apiutil.GVKForObject(obj, r.Scheme())
	if err != nil {
		panic(fmt.Errorf("getting GVK for obj: %w", err))
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)
}

func (r *Reactive) convertWithTypeMeta(in, out runtime.Object) error {
	if err := r.Scheme().Convert(in, out, nil); err != nil {
		return err
	}
	// Scheme().Convert() intentionally "clear[s] TypeMeta to match legacy reflective conversion".
	// We want to keep it to match controller-runtime Clients' behavior.
	out.GetObjectKind().SetGroupVersionKind(in.GetObjectKind().GroupVersionKind())
	return nil

}

func (r *Reactive) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	action := testing.NewGetAction(r.gvrForObject(obj), key.Namespace, key.Name)
	retrievedObj, err := r.Fake.Invokes(action, nil)
	if err != nil {
		return err
	}
	return r.convertWithTypeMeta(retrievedObj, obj)
}

func (r *Reactive) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := client.ListOptions{}
	listOpts.ApplyOptions(opts)

	gvk, listGvk, err := listGVK(list, r.Scheme())
	gvr, _ := meta.UnsafeGuessKindToResource(gvk)

	action := testing.NewListAction(gvr, listGvk, listOpts.Namespace, *listOpts.AsListOptions())
	retrievedObj, err := r.Fake.Invokes(action, nil)
	if err != nil {
		return err
	}
	return r.convertWithTypeMeta(retrievedObj, list)
}

func (r *Reactive) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if len(opts) != 0 {
		panic("testingclient.Reactive doesn't currently handle Create opts")
	}
	object, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("failed creating object: %w", err)
	}

	r.populateGVK(obj)

	action := testing.NewCreateAction(r.gvrForObject(obj), object.GetNamespace(), obj)
	_, err = r.Fake.Invokes(action, nil)
	return err
}

func (r *Reactive) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	// TODO: We are just dropping these options on the floor... this is the same thing
	//       that the controller-runtime fake client does, so it doesn't seem too unusual
	//       but is that really the right thing to do here?
	deleteOpts := client.DeleteOptions{}
	deleteOpts.ApplyOptions(opts)

	action := testing.NewDeleteAction(r.gvrForObject(obj), obj.GetNamespace(), obj.GetName())
	_, err := r.Fake.Invokes(action, nil)
	return err
}

func (r *Reactive) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	panic("implement me")
}

func (r *Reactive) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if len(opts) != 0 {
		panic("testingclient.Reactive doesn't currently handle Update opts")
	}

	r.populateGVK(obj)

	action := testing.NewUpdateAction(r.gvrForObject(obj), obj.GetNamespace(), obj)
	_, err := r.Fake.Invokes(action, nil)
	return err
}

func (r *Reactive) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if len(opts) != 0 {
		panic("testingclient.Reactive doesn't currently handle Patch opts")
	}
	p, err := patch.Data(obj)
	if err != nil {
		return fmt.Errorf("failed patching object: %w", err)
	}
	action := testing.NewPatchAction(r.gvrForObject(obj), obj.GetNamespace(), obj.GetName(), patch.Type(), p)
	_, err = r.Fake.Invokes(action, nil)
	return err
}

func (r *Reactive) Status() client.StatusWriter {
	return r
}
