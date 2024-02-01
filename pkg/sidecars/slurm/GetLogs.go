package slurm

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/containerd/containerd/log"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func (h *SidecarHandler) GetLogsHandler(w http.ResponseWriter, r *http.Request) {
	log.G(h.Ctx).Info("Docker Sidecar: received GetLogs call")
	var req commonIL.LogStruct
	statusCode := http.StatusOK
	currentTime := time.Now()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while checking log requests raw message. Check Docker Sidecar's logs"))
		log.G(h.Ctx).Error(err)
		return
	}

	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		w.Write([]byte("Some errors occurred while unmarshalling log request. Check Docker Sidecar's logs"))
		log.G(h.Ctx).Error(err)
		return
	}

	path := h.Config.DataRootFolder + req.Namespace + "-" + req.PodUID
	var output []byte
	if req.Opts.Timestamps {
		log.G(h.Ctx).Error(errors.New("Not Implemented"))
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		return
	} else {
		log.G(h.Ctx).Info("Reading  " + path + "/" + req.ContainerName + ".out")
		output, err = os.ReadFile(path + "/" + req.ContainerName + ".out")
		if err != nil {
			log.G(h.Ctx).Info("Failed to read container logs, falling back to job log.")
			output, err = os.ReadFile(path + "/" + "job.out")
			if err != nil {
				statusCode = http.StatusInternalServerError
				w.WriteHeader(statusCode)
				return
			}
		}
	}

	var returnedLogs string

	if req.Opts.Tail != 0 {
		var lastLines []string

		splittedLines := strings.Split(string(output), "\n")

		if req.Opts.Tail > len(splittedLines) {
			lastLines = splittedLines
		} else {
			lastLines = splittedLines[len(splittedLines)-req.Opts.Tail-1:]
		}

		for _, line := range lastLines {
			returnedLogs += line + "\n"
		}
	} else if req.Opts.LimitBytes != 0 {
		var lastBytes []byte
		if req.Opts.LimitBytes > len(output) {
			lastBytes = output
		} else {
			lastBytes = output[len(output)-req.Opts.LimitBytes-1:]
		}

		returnedLogs = string(lastBytes)
	} else {
		returnedLogs = string(output)
	}

	if req.Opts.Timestamps && (req.Opts.SinceSeconds != 0 || !req.Opts.SinceTime.IsZero()) {
		temp := returnedLogs
		returnedLogs = ""
		splittedLogs := strings.Split(temp, "\n")
		timestampFormat := "2006-01-02T15:04:05.999999999Z"

		for _, Log := range splittedLogs {
			part := strings.SplitN(Log, " ", 2)
			timestampString := part[0]
			timestamp, err := time.Parse(timestampFormat, timestampString)
			if err != nil {
				continue
			}
			if req.Opts.SinceSeconds != 0 {
				if currentTime.Sub(timestamp).Seconds() > float64(req.Opts.SinceSeconds) {
					returnedLogs += Log + "\n"
				}
			} else {
				if timestamp.Sub(req.Opts.SinceTime).Seconds() >= 0 {
					returnedLogs += Log + "\n"
				}
			}
		}
	}

	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
	} else {
		w.WriteHeader(statusCode)
		w.Write([]byte(returnedLogs))
	}
}
