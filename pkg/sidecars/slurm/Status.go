package slurm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	exec "github.com/alexellis/go-execute/pkg/v1"
	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func (h *SidecarHandler) StatusHandler(w http.ResponseWriter, r *http.Request) {
	var req []*v1.Pod
	var resp []commonIL.PodStatus
	statusCode := http.StatusOK
	log.G(h.Ctx).Info("Slurm Sidecar: received GetStatus call")
	timeNow := time.Now()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
		log.G(h.Ctx).Error(err)
		return
	}

	if timeNow.Sub(timer) >= time.Second*10 {

		err = json.Unmarshal(bodyBytes, &req)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
			log.G(h.Ctx).Error(err)
			return
		}
		cmd := []string{"--me"}
		shell := exec.ExecTask{
			Command: "squeue",
			Args:    cmd,
			Shell:   true,
		}
		execReturn, _ := shell.Execute()
		execReturn.Stdout = strings.ReplaceAll(execReturn.Stdout, "\n", "")

		if execReturn.Stderr != "" {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			w.Write([]byte("Error executing Squeue. Check Slurm Sidecar's logs"))
			log.G(h.Ctx).Error("Unable to retrieve job status: " + execReturn.Stderr)
			return
		}

		for _, pod := range req {
			containerStatuses := []v1.ContainerStatus{}
			uid := string(pod.UID)
			path := h.Config.DataRootFolder + pod.Namespace + "-" + string(pod.UID)

			if checkIfJidExists((h.JIDs), uid) {
				cmd := []string{"--noheader", "-a", "-j " + (*h.JIDs)[uid].JID}
				shell := exec.ExecTask{
					Command: h.Config.Squeuepath,
					Args:    cmd,
					Shell:   true,
				}
				execReturn, _ := shell.Execute()
				timeNow = time.Now()

				//log.G(h.Ctx).Info("Pod: " + jid.PodUID + " | JID: " + jid.JID)

				if execReturn.Stderr != "" {
					log.G(h.Ctx).Error("ERR: ", execReturn.Stderr)
					for _, ct := range pod.Spec.Containers {
						log.G(h.Ctx).Info("Getting exit status from  " + path + "/" + ct.Name + ".status")
						file, err := os.Open(path + "/" + ct.Name + ".status")
						if err != nil {
							statusCode = http.StatusInternalServerError
							w.WriteHeader(statusCode)
							w.Write([]byte("Error retrieving container status. Check Slurm Sidecar's logs"))
							log.G(h.Ctx).Error(fmt.Errorf("unable to retrieve container status: %s", err))
							return
						}
						defer file.Close()
						statusb, err := io.ReadAll(file)
						if err != nil {
							statusCode = http.StatusInternalServerError
							w.WriteHeader(statusCode)
							w.Write([]byte("Error reading container status. Check Slurm Sidecar's logs"))
							log.G(h.Ctx).Error(fmt.Errorf("unable to read container status: %s", err))
							return
						}

						status, err := strconv.Atoi(strings.Replace(string(statusb), "\n", "", -1))
						if err != nil {
							statusCode = http.StatusInternalServerError
							w.WriteHeader(statusCode)
							w.Write([]byte("Error converting container status.. Check Slurm Sidecar's logs"))
							log.G(h.Ctx).Error(fmt.Errorf("unable to convert container status: %s", err))
							status = 500
						}

						containerStatuses = append(
							containerStatuses,
							v1.ContainerStatus{
								Name: ct.Name,
								State: v1.ContainerState{
									Terminated: &v1.ContainerStateTerminated{
										ExitCode: int32(status),
									},
								},
								Ready: false,
							},
						)

					}

					resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
				} else {
					pattern := `(CD|CG|F|PD|PR|R|S|ST)`
					re := regexp.MustCompile(pattern)
					match := re.FindString(execReturn.Stdout)

					log.G(h.Ctx).Info("JID: " + (*h.JIDs)[uid].JID + " | Status: " + match + " | Pod: " + pod.Name + " | UID: " + string(pod.UID))

					switch match {
					case "CD":
						if (*h.JIDs)[uid].EndTime.IsZero() {
							(*h.JIDs)[uid].EndTime = timeNow
							f, err := os.Create(path + "/FinishedAt.time")
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
								log.G(h.Ctx).Error(err)
								return
							}
							f.WriteString((*h.JIDs)[uid].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
						}
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{Time: (*h.JIDs)[uid].StartTime}, FinishedAt: metav1.Time{Time: (*h.JIDs)[uid].EndTime}}}, Ready: false}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					case "CG":
						if (*h.JIDs)[uid].StartTime.IsZero() {
							(*h.JIDs)[uid].StartTime = timeNow
							f, err := os.Create(path + "/StartedAt.time")
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error writing start timestamp... Check Slurm Sidecar's logs"))
								log.G(h.Ctx).Error(err)
								return
							}
							f.WriteString((*h.JIDs)[uid].StartTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
						}
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Running: &v1.ContainerStateRunning{StartedAt: metav1.Time{Time: (*h.JIDs)[uid].StartTime}}}, Ready: true}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					case "F":
						if (*h.JIDs)[uid].EndTime.IsZero() {
							(*h.JIDs)[uid].EndTime = timeNow
							f, err := os.Create(path + "/FinishedAt.time")
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
								log.G(h.Ctx).Error(err)
								return
							}
							f.WriteString((*h.JIDs)[uid].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
						}
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{Time: (*h.JIDs)[uid].StartTime}, FinishedAt: metav1.Time{Time: (*h.JIDs)[uid].EndTime}}}, Ready: false}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					case "PD":
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}}, Ready: false}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					case "PR":
						if (*h.JIDs)[uid].EndTime.IsZero() {
							(*h.JIDs)[uid].EndTime = timeNow
							f, err := os.Create(path + "/FinishedAt.time")
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
								log.G(h.Ctx).Error(err)
								return
							}
							f.WriteString((*h.JIDs)[uid].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
						}
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{Time: (*h.JIDs)[uid].StartTime}, FinishedAt: metav1.Time{Time: (*h.JIDs)[uid].EndTime}}}, Ready: false}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					case "R":
						if (*h.JIDs)[uid].StartTime.IsZero() {
							(*h.JIDs)[uid].StartTime = timeNow
							f, err := os.Create(path + "/StartedAt.time")
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error writing start timestamp... Check Slurm Sidecar's logs"))
								log.G(h.Ctx).Error(err)
								return
							}
							f.WriteString((*h.JIDs)[uid].StartTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
						}
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Running: &v1.ContainerStateRunning{StartedAt: metav1.Time{Time: (*h.JIDs)[uid].StartTime}}}, Ready: true}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					case "S":
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}}, Ready: false}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					case "ST":
						if (*h.JIDs)[uid].EndTime.IsZero() {
							(*h.JIDs)[uid].EndTime = timeNow
							f, err := os.Create(path + "/FinishedAt.time")
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
								log.G(h.Ctx).Error(err)
								return
							}
							f.WriteString((*h.JIDs)[uid].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
						}
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{Time: (*h.JIDs)[uid].StartTime}, FinishedAt: metav1.Time{Time: (*h.JIDs)[uid].EndTime}}}, Ready: false}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					default:
						if (*h.JIDs)[uid].EndTime.IsZero() {
							(*h.JIDs)[uid].EndTime = timeNow
							f, err := os.Create(path + "/FinishedAt.time")
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
								log.G(h.Ctx).Error(err)
								return
							}
							f.WriteString((*h.JIDs)[uid].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
						}
						for _, ct := range pod.Spec.Containers {
							containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{Time: (*h.JIDs)[uid].StartTime}, FinishedAt: metav1.Time{Time: (*h.JIDs)[uid].EndTime}}}, Ready: false}
							containerStatuses = append(containerStatuses, containerStatus)
						}
						resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
					}
				}
			} else {
				for _, ct := range pod.Spec.Containers {
					containerStatus := v1.ContainerStatus{Name: ct.Name, State: v1.ContainerState{}, Ready: false}
					containerStatuses = append(containerStatuses, containerStatus)
				}
				resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: containerStatuses})
			}

		}
		cachedStatus = resp
		timer = time.Now()
	} else {
		log.G(h.Ctx).Debug("Cached status")
		resp = cachedStatus
	}

	log.G(h.Ctx).Debug(resp)

	w.WriteHeader(statusCode)
	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred deleting containers. Check Docker Sidecar's logs"))
	} else {
		bodyBytes, err := json.Marshal(resp)
		if err != nil {
			w.WriteHeader(statusCode)
			w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
			log.G(h.Ctx).Error(err)
			return
		}
		w.Write(bodyBytes)
	}
}
