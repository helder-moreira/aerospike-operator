/*
Copyright 2018 The aerospike-controller Authors.

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

package controller

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	aerospikeclientset "github.com/travelaudience/aerospike-operator/pkg/client/clientset/versioned"
	aerospikescheme "github.com/travelaudience/aerospike-operator/pkg/client/clientset/versioned/scheme"
	aerospikeinformers "github.com/travelaudience/aerospike-operator/pkg/client/informers/externalversions"
	aerospikelisters "github.com/travelaudience/aerospike-operator/pkg/client/listers/aerospike/v1alpha1"
	"github.com/travelaudience/aerospike-operator/pkg/reconciler"
)

const controllerAgentName = "aerospike"

// AerospikeClusterController is the controller implementation for AerospikeCluster resources
type AerospikeClusterController struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// aerospikeclientset is a clientset for our own API group
	aerospikeclientset aerospikeclientset.Interface

	podsLister              corelisters.PodLister
	configMapsLister        corelisters.ConfigMapLister
	servicesLister          corelisters.ServiceLister
	podsSynced              cache.InformerSynced
	configMapsSynced        cache.InformerSynced
	servicesSynced          cache.InformerSynced
	aerospikeClustersLister aerospikelisters.AerospikeClusterLister
	aerospikeClustersSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder

	reconciler *reconciler.AerospikeClusterReconciler
}

// NewController returns a new aerospike controller
func NewAerospikeClusterController(
	kubeclientset kubernetes.Interface,
	aerospikeclientset aerospikeclientset.Interface,
	kubeInformerFactory kubeinformers.SharedInformerFactory,
	aerospikeInformerFactory aerospikeinformers.SharedInformerFactory) *AerospikeClusterController {

	// obtain references to shared index informers for the Pod and AerospikeCluster
	// types.
	podInformer := kubeInformerFactory.Core().V1().Pods()
	configMapInformer := kubeInformerFactory.Core().V1().ConfigMaps()
	serviceInformer := kubeInformerFactory.Core().V1().Services()
	aerospikeClusterInformer := aerospikeInformerFactory.Aerospike().V1alpha1().AerospikeClusters()

	// Create event broadcaster
	// Add aerospike types to the default Kubernetes Scheme so Events can be
	// logged for aerospike types.
	aerospikescheme.AddToScheme(scheme.Scheme)
	log.Debug("creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(log.Debugf)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	// obtain references to lister for the Pod and AerospikeClusters types
	podsLister := podInformer.Lister()
	configMapsLister := configMapInformer.Lister()
	servicesLister := serviceInformer.Lister()
	aerospikeClustersLister := aerospikeClusterInformer.Lister()

	controller := &AerospikeClusterController{
		kubeclientset:           kubeclientset,
		aerospikeclientset:      aerospikeclientset,
		podsLister:              podsLister,
		configMapsLister:        configMapsLister,
		servicesLister:          servicesLister,
		podsSynced:              podInformer.Informer().HasSynced,
		configMapsSynced:        configMapInformer.Informer().HasSynced,
		servicesSynced:          serviceInformer.Informer().HasSynced,
		aerospikeClustersLister: aerospikeClustersLister,
		aerospikeClustersSynced: aerospikeClusterInformer.Informer().HasSynced,
		workqueue:               workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "AerospikeClusters"),
		recorder:                recorder,
		reconciler:              reconciler.New(kubeclientset, aerospikeclientset, podsLister, configMapsLister, servicesLister, recorder),
	}

	log.Debug("setting up event handlers")
	// Set up an event handler for when AerospikeCluster resources change
	aerospikeClusterInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueAerospikeCluster,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueAerospikeCluster(new)
		},
	})
	// Set up an event handler for when Pod resources change. This
	// handler will lookup the owner of the given Pod, and if it is
	// owned by a AerospikeCluster resource will enqueue that AerospikeCluster resource for
	// processing. This way, we don't need to implement custom logic for
	// handling Pod resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newPod := new.(*corev1.Pod)
			oldPod := old.(*corev1.Pod)
			if newPod.ResourceVersion == oldPod.ResourceVersion {
				// Periodic resync will send update events for all known Pods.
				// Two different versions of the same Pod will always have different RVs.
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *AerospikeClusterController) Run(threadiness int, stopCh <-chan struct{}) error {
	defer runtime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	log.Debug("starting controller")

	// Wait for the caches to be synced before starting workers
	log.Debug("waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.podsSynced, c.aerospikeClustersSynced, c.configMapsSynced, c.servicesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	log.Debug("starting workers")
	// Launch two workers to process AerospikeCluster resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	log.Debug("started workers")
	<-stopCh
	log.Debug("shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *AerospikeClusterController) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *AerospikeClusterController) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// AerospikeCluster resource to be synced.
		if err := c.syncHandler(key); err != nil {
			return fmt.Errorf("error syncing '%s': %s", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		log.Debugf("successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		runtime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the AerospikeCluster resource
// with the current status of the resource.
func (c *AerospikeClusterController) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the AerospikeCluster resource with this namespace/name
	aerospikeCluster, err := c.aerospikeClustersLister.AerospikeClusters(namespace).Get(name)
	if err != nil {
		// The AerospikeCluster resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			runtime.HandleError(fmt.Errorf("aerospikecluster '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	// deepcopy aerospikeCluster before reconciling so we don't possibly mutate the cache
	err = c.reconciler.MaybeReconcile(aerospikeCluster.DeepCopy())
	if err != nil {
		return err
	}
	return nil
}

// enqueueAerospikeCluster takes a AerospikeCluster resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than AerospikeCluster.
func (c *AerospikeClusterController) enqueueAerospikeCluster(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	c.workqueue.AddRateLimited(key)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the AerospikeCluster resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that AerospikeCluster resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *AerospikeClusterController) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			runtime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			runtime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		log.Debugf("recovered deleted object '%s' from tombstone", object.GetName())
	}
	log.Debugf("processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a AerospikeCluster, we should not do anything more
		// with it.
		if ownerRef.Kind != "AerospikeCluster" {
			return
		}

		aerospikeCluster, err := c.aerospikeClustersLister.AerospikeClusters(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			log.Debugf("ignoring orphaned object '%s' of aerospikeCluster '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}

		c.enqueueAerospikeCluster(aerospikeCluster)
		return
	}
}
