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
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	var req []*v1.Pod
	var resp []commonIL.PodStatus
	statusCode := http.StatusOK
	log.G(Ctx).Info("Slurm Sidecar: received GetStatus call")
	timeNow := time.Now()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
		log.G(Ctx).Error(err)
		return
	}

	if timeNow.Sub(timer) >= time.Second*10 {

		json.Unmarshal(bodyBytes, &req)
		if err != nil {
			statusCode = http.StatusInternalServerError
			w.WriteHeader(statusCode)
			w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
			log.G(Ctx).Error(err)
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
			log.G(Ctx).Error("Unable to retrieve job status: " + execReturn.Stderr)
			return
		}

		for _, pod := range req {
			for i, jid := range JIDs {
				if jid.PodUID == string(pod.UID) {
					cmd := []string{"--noheader", "-a", "-j " + jid.JID}
					shell := exec.ExecTask{
						Command: commonIL.InterLinkConfigInst.Squeuepath,
						Args:    cmd,
						Shell:   true,
					}
					execReturn, _ := shell.Execute()
					timeNow = time.Now()

					if execReturn.Stderr != "" {
						containerStatuses := []v1.ContainerStatus{}
						for _, ct := range pod.Spec.Containers {
							log.G(Ctx).Info("Getting exit status from  " + commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/" + ct.Name + ".status")
							file, err := os.Open(commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/" + ct.Name + ".status")
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error retrieving container status. Check Slurm Sidecar's logs"))
								log.G(Ctx).Error(fmt.Errorf("unable to retrieve container status: %s", err))
								return
							}
							defer file.Close()
							statusb, err := io.ReadAll(file)
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error reading container status. Check Slurm Sidecar's logs"))
								log.G(Ctx).Error(fmt.Errorf("unable to read container status: %s", err))
								return
							}

							status, err := strconv.Atoi(strings.Replace(string(statusb), "\n", "", -1))
							if err != nil {
								statusCode = http.StatusInternalServerError
								w.WriteHeader(statusCode)
								w.Write([]byte("Error converting container status.. Check Slurm Sidecar's logs"))
								log.G(Ctx).Error(fmt.Errorf("unable to convert container status: %s", err))
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

						log.G(Ctx).Info("JID: " + jid.JID + " | Status: " + match + " | Pod: " + pod.Name + " | UID: " + string(pod.UID) + " Time: " + string(timeNow.Format("2006-01-02 15:04:05.999999999 -0700 MST")))

						switch match {
						case "CD":
							if jid.EndTime.IsZero() {
								JIDs[i].EndTime = timeNow
								f, err := os.Create(commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/FinishedAt.time")
								if err != nil {
									statusCode = http.StatusInternalServerError
									w.WriteHeader(statusCode)
									w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
									log.G(Ctx).Error(err)
									return
								}
								f.WriteString(JIDs[i].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
							}
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{JIDs[i].StartTime}, FinishedAt: metav1.Time{JIDs[i].EndTime}}}, Ready: false}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						case "CG":
							if jid.StartTime.IsZero() {
								JIDs[i].StartTime = timeNow
								f, err := os.Create(commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/StartedAt.time")
								if err != nil {
									statusCode = http.StatusInternalServerError
									w.WriteHeader(statusCode)
									w.Write([]byte("Error writing start timestamp... Check Slurm Sidecar's logs"))
									log.G(Ctx).Error(err)
									return
								}
								f.WriteString(JIDs[i].StartTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
							}
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Running: &v1.ContainerStateRunning{StartedAt: metav1.Time{JIDs[i].StartTime}}}, Ready: true}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						case "F":
							if jid.EndTime.IsZero() {
								JIDs[i].EndTime = timeNow
								f, err := os.Create(commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/FinishedAt.time")
								if err != nil {
									statusCode = http.StatusInternalServerError
									w.WriteHeader(statusCode)
									w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
									log.G(Ctx).Error(err)
									return
								}
								f.WriteString(JIDs[i].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
							}
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{JIDs[i].StartTime}, FinishedAt: metav1.Time{JIDs[i].EndTime}}}, Ready: false}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						case "PD":
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}}, Ready: false}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						case "PR":
							if jid.EndTime.IsZero() {
								JIDs[i].EndTime = timeNow
								f, err := os.Create(commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/FinishedAt.time")
								if err != nil {
									statusCode = http.StatusInternalServerError
									w.WriteHeader(statusCode)
									w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
									log.G(Ctx).Error(err)
									return
								}
								f.WriteString(JIDs[i].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
							}
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{JIDs[i].StartTime}, FinishedAt: metav1.Time{JIDs[i].EndTime}}}, Ready: false}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						case "R":
							if jid.StartTime.IsZero() {
								JIDs[i].StartTime = timeNow
								f, err := os.Create(commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/StartedAt.time")
								if err != nil {
									statusCode = http.StatusInternalServerError
									w.WriteHeader(statusCode)
									w.Write([]byte("Error writing start timestamp... Check Slurm Sidecar's logs"))
									log.G(Ctx).Error(err)
									return
								}
								f.WriteString(JIDs[i].StartTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
							}
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Running: &v1.ContainerStateRunning{StartedAt: metav1.Time{JIDs[i].StartTime}}}, Ready: true}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						case "S":
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}}, Ready: false}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						case "ST":
							if jid.EndTime.IsZero() {
								JIDs[i].EndTime = timeNow
								f, err := os.Create(commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/FinishedAt.time")
								if err != nil {
									statusCode = http.StatusInternalServerError
									w.WriteHeader(statusCode)
									w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
									log.G(Ctx).Error(err)
									return
								}
								f.WriteString(JIDs[i].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
							}
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{JIDs[i].StartTime}, FinishedAt: metav1.Time{JIDs[i].EndTime}}}, Ready: false}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						default:
							if jid.EndTime.IsZero() {
								JIDs[i].EndTime = timeNow
								f, err := os.Create(commonIL.InterLinkConfigInst.DataRootFolder + string(pod.UID) + "/FinishedAt.time")
								if err != nil {
									statusCode = http.StatusInternalServerError
									w.WriteHeader(statusCode)
									w.Write([]byte("Error writing end timestamp... Check Slurm Sidecar's logs"))
									log.G(Ctx).Error(err)
									return
								}
								f.WriteString(JIDs[i].EndTime.Format("2006-01-02 15:04:05.999999999 -0700 MST"))
							}
							containerStatus := v1.ContainerStatus{Name: pod.Spec.Containers[0].Name, State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{StartedAt: metav1.Time{JIDs[i].StartTime}, FinishedAt: metav1.Time{JIDs[i].EndTime}}}, Ready: false}
							resp = append(resp, commonIL.PodStatus{PodName: pod.Name, PodUID: string(pod.UID), PodNamespace: pod.Namespace, Containers: []v1.ContainerStatus{containerStatus}})
						}
					}
				}
			}
		}
		cachedStatus = resp
		timer = time.Now()
	} else {
		log.G(Ctx).Debug("Cached status")
		resp = cachedStatus
	}

	log.G(Ctx).Debug(resp)

	w.WriteHeader(statusCode)
	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred deleting containers. Check Docker Sidecar's logs"))
	} else {
		bodyBytes, err := json.Marshal(resp)
		if err != nil {
			w.WriteHeader(statusCode)
			w.Write([]byte("Some errors occurred while retrieving container status. Check Slurm Sidecar's logs"))
			log.G(Ctx).Error(err)
			return
		}
		w.Write(bodyBytes)
	}
}
