package virtualkubelet

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultShadowPauseImage keeps a shadow alive when the pod keeps only init
// containers on the cluster, so the Deployment has a container to run once they end.
const defaultShadowPauseImage = "registry.k8s.io/pause:3.10"

// keptInitPollInterval is how often the shadow is checked while its init containers run.
var keptInitPollInterval = 2 * time.Second

// errPodGoneWhileInitializing tells the caller the pod was deleted before its
// cluster-side init containers finished, so there is nothing left to submit.
var errPodGoneWhileInitializing = errors.New("pod deleted while its cluster-side init containers were running")

// hasKeptContainers reports whether the pod keeps any container on the cluster
// (interlink.eu/disable-offload-containers, interlink.eu/disable-offload-init-containers).
// Those containers only ever run inside the shadow.
func hasKeptContainers(pod *v1.Pod) bool {
	return len(getLocalContainers(pod)) > 0 || len(getLocalInitContainers(pod)) > 0
}

// hasTunnelShadow reports whether the pod's shadow must also carry a tunnel or the
// mesh, as opposed to a shadow that only runs the containers kept on the cluster.
func (p *Provider) hasTunnelShadow(pod *v1.Pod) bool {
	return p.shouldCreateShadow(pod) || (p.config.Network.FullMesh && !isMeshNetworkingDisabled(pod))
}

func (p *Provider) shadowPauseImage() string {
	if p.config.ShadowPauseImage != "" {
		return p.config.ShadowPauseImage
	}
	return defaultShadowPauseImage
}

// dropKeptOnlyVolumes returns the pod's volumes without those that only the containers
// kept on the cluster mount: they belong to the shadow, and sending them to the plugin
// would copy their contents (a credential, say) to the remote side for nothing.
func dropKeptOnlyVolumes(pod, offloaded *v1.Pod) []v1.Volume {
	if !hasKeptContainers(pod) {
		return offloaded.Spec.Volumes
	}
	mountedBy := func(containers []v1.Container) map[string]bool {
		names := map[string]bool{}
		for _, c := range containers {
			for _, m := range c.VolumeMounts {
				names[m.Name] = true
			}
		}
		return names
	}
	kept := mountedBy(append(getLocalContainers(pod), getLocalInitContainers(pod)...))
	remote := mountedBy(append(append([]v1.Container{}, offloaded.Spec.Containers...), offloaded.Spec.InitContainers...))

	volumes := make([]v1.Volume, 0, len(offloaded.Spec.Volumes))
	for _, v := range offloaded.Spec.Volumes {
		if kept[v.Name] && !remote[v.Name] {
			continue
		}
		volumes = append(volumes, v)
	}
	return volumes
}

// keptInitState summarises the cluster-side init containers of a shadow pod.
type keptInitState struct {
	statuses []v1.ContainerStatus // the shadow's statuses of the kept init containers, in spec order
	done     bool                 // every kept init container ended with exit code 0
	failed   *v1.ContainerStatus  // the first kept init container that exited non-zero, if any
}

// keptInitProgress reads the kept init containers' statuses from the shadow pod.
// A container that failed and is being restarted by the shadow's kubelet counts as
// failed too: it decides the outcome for pods with restartPolicy Never.
func keptInitProgress(shadow *v1.Pod, kept []v1.Container) keptInitState {
	byName := make(map[string]v1.ContainerStatus, len(shadow.Status.InitContainerStatuses))
	for _, s := range shadow.Status.InitContainerStatuses {
		byName[s.Name] = s
	}

	state := keptInitState{done: true}
	for _, c := range kept {
		s, ok := byName[c.Name]
		if !ok {
			state.done = false
			continue
		}
		state.statuses = append(state.statuses, s)

		terminated := s.State.Terminated
		if terminated == nil || terminated.ExitCode != 0 {
			state.done = false
		}
		if state.failed != nil {
			continue
		}
		last := s.LastTerminationState.Terminated
		if (terminated != nil && terminated.ExitCode != 0) || (last != nil && last.ExitCode != 0) {
			failed := s
			state.failed = &failed
		}
	}
	return state
}

// currentShadowPod returns the live pod of the shadow Deployment, or nil while there is none.
func (p *Provider) currentShadowPod(ctx context.Context, identity shadowResourceIdentity) (*v1.Pod, error) {
	pods, err := p.clientSet.CoreV1().Pods(identity.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app.kubernetes.io/component=%s", identity.Name),
	})
	if err != nil {
		return nil, err
	}
	var newest *v1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		if newest == nil || newest.CreationTimestamp.Before(&pod.CreationTimestamp) {
			newest = pod
		}
	}
	return newest, nil
}

// mirrorKeptInitStatuses copies the shadow's statuses of the kept init containers onto
// the offloaded pod, which stays Pending with PodInitialized=False meanwhile. It returns
// a copy to notify, or nil when the pod is not tracked (yet) or nothing changed.
func (p *Provider) mirrorKeptInitStatuses(uid string, statuses []v1.ContainerStatus) (*v1.Pod, bool) {
	p.podsMu.Lock()
	defer p.podsMu.Unlock()

	pod, ok := p.pods[uid]
	if !ok {
		return nil, false
	}

	merged := make([]v1.ContainerStatus, len(pod.Status.InitContainerStatuses))
	copy(merged, pod.Status.InitContainerStatuses)
	for _, s := range statuses {
		replaced := false
		for i := range merged {
			if merged[i].Name == s.Name {
				merged[i] = s
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, s)
		}
	}
	if reflect.DeepEqual(merged, pod.Status.InitContainerStatuses) {
		return nil, true
	}
	pod.Status.InitContainerStatuses = merged
	return pod.DeepCopy(), true
}

// failPodOnKeptInit ends a restartPolicy Never pod whose cluster-side init container
// failed, as the kubelet would, without submitting anything to the remote side.
func (p *Provider) failPodOnKeptInit(ctx context.Context, uid string, failed *v1.ContainerStatus) {
	code := int32(0)
	if failed.State.Terminated != nil {
		code = failed.State.Terminated.ExitCode
	} else if failed.LastTerminationState.Terminated != nil {
		code = failed.LastTerminationState.Terminated.ExitCode
	}
	message := fmt.Sprintf("init container %s, kept on the cluster, exited with code %d", failed.Name, code)

	p.podsMu.Lock()
	pod, ok := p.pods[uid]
	if !ok {
		p.podsMu.Unlock()
		return
	}
	pod.Status.Phase = v1.PodFailed
	pod.Status.Reason = "InitContainerFailed"
	pod.Status.Message = message
	for i := range pod.Status.InitContainerStatuses {
		if pod.Status.InitContainerStatuses[i].Name == failed.Name && pod.Status.InitContainerStatuses[i].State.Terminated == nil {
			pod.Status.InitContainerStatuses[i].State = v1.ContainerState{Terminated: &v1.ContainerStateTerminated{
				ExitCode: code,
				Reason:   "Error",
				Message:  message,
			}}
		}
	}
	notify := pod.DeepCopy()
	p.podsMu.Unlock()

	log.G(ctx).Warningf("Pod %s/%s failed: %s", notify.Namespace, notify.Name, message)
	if err := p.UpdatePod(ctx, notify); err != nil {
		log.G(ctx).Error(err)
	}
}

// waitForKeptInitContainers holds the remote submission until the init containers the
// pod keeps on the cluster have all completed in its shadow, as the kubelet runs init
// containers before the others. Kept init containers therefore run before every
// offloaded one, whatever their order in the spec. Their statuses are mirrored on the
// offloaded pod while it waits. A failure ends a restartPolicy Never pod; with other
// policies the shadow's kubelet retries the container and the wait goes on. There is
// no timeout, as with the kubelet.
func (p *Provider) waitForKeptInitContainers(ctx context.Context, pod *v1.Pod) error {
	kept := getLocalInitContainers(pod)
	if len(kept) == 0 {
		return nil
	}
	identity, err := computeShadowResourceIdentity(pod)
	if err != nil {
		return err
	}
	uid := string(pod.UID)
	log.G(ctx).Infof("Pod %s/%s: waiting for %d init container(s) kept on the cluster before submitting", pod.Namespace, pod.Name, len(kept))

	// CreatePod registers the pod after starting this wait, so a pod not tracked yet is
	// still being created; one that was tracked and is gone has been deleted.
	tracked := false
	for {
		p.podsMu.RLock()
		_, exists := p.pods[uid]
		p.podsMu.RUnlock()
		if exists {
			tracked = true
		} else if tracked {
			return errPodGoneWhileInitializing
		}

		shadow, err := p.currentShadowPod(ctx, identity)
		if err != nil {
			log.G(ctx).Warningf("Failed to read the shadow of %s/%s: %v", pod.Namespace, pod.Name, err)
		} else if shadow != nil {
			state := keptInitProgress(shadow, kept)
			notify, _ := p.mirrorKeptInitStatuses(uid, state.statuses)
			if notify != nil {
				if err := p.UpdatePod(ctx, notify); err != nil {
					log.G(ctx).Error(err)
				}
			}
			if state.failed != nil && pod.Spec.RestartPolicy == v1.RestartPolicyNever {
				p.failPodOnKeptInit(ctx, uid, state.failed)
				return fmt.Errorf("init container %s kept on the cluster failed", state.failed.Name)
			}
			if state.done {
				log.G(ctx).Infof("Pod %s/%s: init containers kept on the cluster completed", pod.Namespace, pod.Name)
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(keptInitPollInterval):
		}
	}
}
