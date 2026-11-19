/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package user

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
)

func passwordSecretUser(name, namespace, secretName, key string) *v1alpha1.User {
	return &v1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.UserSpec{
			ForProvider: v1alpha1.UserParameters{
				Authentication: v1alpha1.Authentication{
					Password: &v1alpha1.Password{
						PasswordSecretRef: &xpv2.SecretKeySelector{
							SecretReference: xpv2.SecretReference{Namespace: namespace, Name: secretName},
							Key:             key,
						},
					},
				},
			},
		},
	}
}

func TestIndexPasswordSecretRef(t *testing.T) {
	tests := map[string]struct {
		user *v1alpha1.User
		want []string
	}{
		"NoPassword": {user: &v1alpha1.User{}},
		"NoSecretReference": {
			user: &v1alpha1.User{
				Spec: v1alpha1.UserSpec{ForProvider: v1alpha1.UserParameters{
					Authentication: v1alpha1.Authentication{Password: &v1alpha1.Password{}},
				}},
			},
		},
		"SecretReference": {
			user: passwordSecretUser("user", "default", "password", "key"),
			want: []string{"default/password"},
		},
		"DifferentNamespace": {
			user: passwordSecretUser("user", "other", "password", "key"),
			want: []string{"other/password"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, indexPasswordSecretRef(tt.user)); diff != "" {
				t.Errorf("indexPasswordSecretRef(): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestSecretMappingUsesReferenceIndex(t *testing.T) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "password"}}
	calls := 0
	kube := &test.MockClient{
		MockList: func(_ context.Context, _ client.ObjectList, opts ...client.ListOption) error {
			calls++
			options := &client.ListOptions{}
			options.ApplyOptions(opts)
			want := passwordSecretRefIndex + "=default/password"
			if options.FieldSelector == nil || options.FieldSelector.String() != want {
				t.Errorf("expected indexed field selector %q, got %v", want, options.FieldSelector)
			}
			if options.Namespace != "" {
				t.Errorf("User resources are cluster-scoped, got namespace restriction %q", options.Namespace)
			}
			return nil
		},
	}
	generateReconcileRequestsFromSecret(context.Background(), secret, kube, logging.NewNopLogger())
	if calls != 1 {
		t.Errorf("expected one indexed List call, got %d", calls)
	}
}

func TestPasswordSecretReferenceLifecycle(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	user := passwordSecretUser("first", "default", "password", "first-key")
	kube := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&v1alpha1.User{}, passwordSecretRefIndex, indexPasswordSecretRef).
		WithObjects(
			user,
			passwordSecretUser("second", "default", "password", "second-key"),
			passwordSecretUser("different-namespace", "other", "password", "key"),
			passwordSecretUser("different-secret", "default", "unrelated", "key"),
			&v1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "no-password"}},
		).Build()

	assertUsers := func(namespace, name string, want ...string) {
		t.Helper()
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
		requests := generateReconcileRequestsFromSecret(ctx, secret, kube, logging.NewNopLogger())
		got := make([]string, 0, len(requests))
		for _, request := range requests {
			got = append(got, request.Name)
		}
		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty(), cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
			t.Errorf("Users referencing %s/%s: -want, +got:\n%s", namespace, name, diff)
		}
	}

	assertUsers("default", "password", "first", "second")
	assertUsers("other", "password", "different-namespace")
	assertUsers("default", "unused")

	if err := kube.Get(ctx, types.NamespacedName{Name: user.Name}, user); err != nil {
		t.Fatal(err)
	}
	user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Name = "replacement"
	if err := kube.Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	assertUsers("default", "password", "second")
	assertUsers("default", "replacement", "first")

	user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Namespace = "other"
	if err := kube.Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	assertUsers("default", "replacement")
	assertUsers("other", "replacement", "first")

	user.Spec.ForProvider.Authentication.Password.PasswordSecretRef = nil
	if err := kube.Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	assertUsers("other", "replacement")

	user.Spec.ForProvider.Authentication.Password.PasswordSecretRef = &xpv2.SecretKeySelector{
		SecretReference: xpv2.SecretReference{Namespace: "default", Name: "password"},
		Key:             "first-key",
	}
	if err := kube.Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	assertUsers("default", "password", "first", "second")

	user.Spec.ForProvider.Authentication.Password = nil
	if err := kube.Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	assertUsers("default", "password", "second")

	if err := kube.Create(ctx, passwordSecretUser("new-user", "default", "password", "key")); err != nil {
		t.Fatal(err)
	}
	assertUsers("default", "password", "new-user", "second")
	if err := kube.Delete(ctx, &v1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "second"}}); err != nil {
		t.Fatal(err)
	}
	assertUsers("default", "password", "new-user")
}

func TestPasswordSecretWatchEvents(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&v1alpha1.User{}, passwordSecretRefIndex, indexPasswordSecretRef).
		WithObjects(passwordSecretUser("user", "default", "password", "key")).Build()
	h := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		return generateReconcileRequestsFromSecret(ctx, obj, kube, logging.NewNopLogger())
	})
	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "password"},
		Data:       map[string][]byte{"key": []byte("old")},
	}
	newSecret := oldSecret.DeepCopy()
	newSecret.Data["key"] = []byte("new")
	tests := map[string]func(context.Context, workqueue.TypedRateLimitingInterface[reconcile.Request]){
		"Create": func(ctx context.Context, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			h.Create(ctx, event.CreateEvent{Object: newSecret}, queue)
		},
		"Update": func(ctx context.Context, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			h.Update(ctx, event.UpdateEvent{ObjectOld: oldSecret, ObjectNew: newSecret}, queue)
		},
		"Delete": func(ctx context.Context, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			h.Delete(ctx, event.DeleteEvent{Object: oldSecret}, queue)
		},
	}
	for name, emit := range tests {
		t.Run(name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
			defer queue.ShutDown()
			emit(context.Background(), queue)
			if queue.Len() != 1 {
				t.Fatalf("expected one User request, got %d", queue.Len())
			}
			request, _ := queue.Get()
			defer queue.Done(request)
			if request.Name != "user" || request.Namespace != "" {
				t.Errorf("unexpected User request: %v", request)
			}
		})
	}
}

type nonStreamingListWatch struct {
	*toolscache.ListWatch
}

func (*nonStreamingListWatch) IsWatchListSemanticsUnSupported() bool { return true }

func TestPasswordSecretInitialEventsAtScale(t *testing.T) {
	const (
		userCount   = 2734
		secretCount = 4537
	)
	users := &v1alpha1.UserList{ListMeta: metav1.ListMeta{ResourceVersion: "1"}}
	for i := range userCount {
		users.Items = append(users.Items, *passwordSecretUser(
			fmt.Sprintf("user-%d", i), "default", fmt.Sprintf("password-%d", i), "key"))
	}
	secrets := &corev1.SecretList{ListMeta: metav1.ListMeta{ResourceVersion: "1"}}
	for i := range secretCount {
		secrets.Items = append(secrets.Items, corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: fmt.Sprintf("password-%d", i)},
		})
	}
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		v1alpha1.UserGroupVersionKind.GroupVersion(), corev1.SchemeGroupVersion,
	})
	mapper.Add(v1alpha1.UserGroupVersionKind, meta.RESTScopeRoot)
	mapper.Add(corev1.SchemeGroupVersion.WithKind("Secret"), meta.RESTScopeNamespace)

	// Use real informer caches and initial event delivery, replacing only API I/O.
	kube, err := cache.New(&rest.Config{Host: "http://localhost"}, cache.Options{
		Scheme: scheme,
		Mapper: mapper,
		NewInformer: func(_ toolscache.ListerWatcher, obj runtime.Object, resync time.Duration, indexes toolscache.Indexers) toolscache.SharedIndexInformer {
			lw := &nonStreamingListWatch{ListWatch: &toolscache.ListWatch{
				ListWithContextFunc: func(context.Context, metav1.ListOptions) (runtime.Object, error) {
					if _, ok := obj.(*v1alpha1.User); ok {
						return users, nil
					}
					return secrets, nil
				},
				WatchFuncWithContext: func(context.Context, metav1.ListOptions) (watch.Interface, error) {
					return watch.NewRaceFreeFake(), nil
				},
			}}
			return toolscache.NewSharedIndexInformer(lw, obj, resync, indexes)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := kube.IndexField(ctx, &v1alpha1.User{}, passwordSecretRefIndex, indexPasswordSecretRef); err != nil {
		t.Fatal(err)
	}
	informer, err := kube.GetInformer(ctx, &corev1.Secret{})
	if err != nil {
		t.Fatal(err)
	}
	var events, requests atomic.Int64
	registration, err := informer.AddEventHandlerWithOptions(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			requests.Add(int64(len(generateReconcileRequestsFromSecret(ctx, obj.(*corev1.Secret), kube, logging.NewNopLogger()))))
			events.Add(1)
		},
	}, toolscache.HandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- kube.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("cache shutdown: %v", err)
		}
	})
	if !toolscache.WaitForCacheSync(ctx.Done(), registration.HasSynced) {
		t.Fatal("initial Secret events did not finish within the default 120-second startup timeout")
	}
	if got := events.Load(); got != secretCount {
		t.Errorf("expected %d initial Secret events, got %d", secretCount, got)
	}
	if got := requests.Load(); got != userCount {
		t.Errorf("expected %d User requests, got %d", userCount, got)
	}
	t.Logf("Mapped %d initial Secret events to %d Users in %s", secretCount, userCount, time.Since(start))
}
