package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func NewKubeConfig() *rest.Config {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)
	config, err := kubeConfig.ClientConfig()
	if err != nil {
		panic(err)
	}

	return config

}

// NewKubeClient create a new kubernetes clients from environment
// panics if this is not possible
func NewKubeClient() *kubernetes.Clientset {

	client, err := kubernetes.NewForConfig(NewKubeConfig())
	if err != nil {
		panic(err)
	}

	return client
}

func EnsurePodFinalizer(kube *kubernetes.Clientset, pod *corev1.Pod) error {

	if HasPodFinalizer(pod) {
		return nil
	}

	pod.SetFinalizers(append(pod.GetFinalizers(), FinalizerNameString))

	// Otherwise try to patch
	//cmd := fmt.Sprintf(`[{"op":"add", "path":"/metadata/finalizers/0", "value":"%s"}]`, FinalizerNameString)
	//fmt.Printf("cmd: %s\n", cmd)
	//_, err := kube.CoreV1().Pods(namespace).Patch(pod.GetName(), types.JSONPatchType, []byte(cmd))
	//if err != nil {
	if _, err := kube.CoreV1().Pods(namespace).Update(pod); err != nil {
		return err
	}

	fmt.Printf("Added finalizer to %s\n", pod.GetName())
	return nil
}

func HasPodFinalizer(pod *corev1.Pod) bool {
	contains := func(a []string, x string) bool {
		for _, n := range a {
			if x == n {
				return true
			}
		}
		return false
	}

	return contains(pod.GetFinalizers(), FinalizerNameString)
}

func RemovePodFinalizer(kube *kubernetes.Clientset, pod *corev1.Pod) error {

	if !HasPodFinalizer(pod) {
		return nil
	}

	var newFinalizers []string
	for _, f := range pod.GetFinalizers() {
		if f != FinalizerNameString {
			newFinalizers = append(newFinalizers, f)
		}
	}

	pod.SetFinalizers(newFinalizers)

	// Otherwise try to patch
	_, err := kube.CoreV1().Pods(namespace).Update(pod)
	if err != nil {
		return err
	}

	fmt.Printf("Released finalizer on %s\n", pod.GetName())
	return nil
}

func InspectPod(kube *kubernetes.Clientset, pod *corev1.Pod) error {

	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {

		if !HasPodFinalizer(pod) {
			return nil
		}

		logstream, err := kube.CoreV1().Pods(namespace).GetLogs(pod.GetName(), &corev1.PodLogOptions{Container: "server"}).Stream()
		if err != nil {
			return err
		}
		defer logstream.Close()

		logFilename := path.Join(logDirectory, fmt.Sprintf("%s_%s.log", pod.GetCreationTimestamp().UTC().Format(time.RFC3339), pod.GetName()))

		logf, err := os.Create(logFilename)
		if err != nil {
			return err
		}

		fmt.Printf("Receiving log for pod %s\n", pod.GetName())

		if _, err := io.Copy(logf, logstream); err != nil {
			return err
		}

		fmt.Printf("Completed log for pod %s\n", pod.GetName())

		if err := RemovePodFinalizer(kube, pod); err != nil {
			return err
		}
	} else if pod.GetDeletionTimestamp() == nil {
		if err := EnsurePodFinalizer(kube, pod); err != nil {
			fmt.Printf("Failed to ensure finalizer: %s\n", err.Error())
		}
	}

	return nil
}

const (
	FinalizerNameString = "logs.database.arangodb.com/receive-log"
)

var (
	namespace          string
	logDirectory       string
	restrictDeployment string
)

func init() {
	flag.StringVar(&namespace, "namespace", "default", "namespace")
	flag.StringVar(&logDirectory, "log-directory", "logs", "file directory to store log file")
	flag.StringVar(&restrictDeployment, "deployment", "", "use to restrict logging to a specific deployment, leave empty to select all deployments")
}

func main() {
	flag.Parse()
	kube := NewKubeClient()

	fmt.Printf("Using namespace %s\n", namespace)
	fmt.Printf("Putting logs into %s\n", logDirectory)

	// lets create a
	watcher, err := kube.CoreV1().Pods(namespace).Watch(metav1.ListOptions{
		LabelSelector: "app=arangodb",
	})
	if err != nil {
		panic(err)
	}

	http.HandleFunc("logs", func(resp http.ResponseWriter, req *http.Request) {
		filename := req.FormValue("name")
		if filename != "" {

		} else {
			// list directory
		}
	})

	go log.Fatal(http.ListenAndServe(":8080", nil))

	fmt.Println("Up and running")

	for {
		select {
		case ev := <-watcher.ResultChan():
			if pod, ok := ev.Object.(*corev1.Pod); ok {
				switch ev.Type {
				case watch.Added, watch.Modified:
					// pod is marked for deletion
					if err := InspectPod(kube, pod); err != nil {
						fmt.Printf("Pod inspection failed: %s\n", err.Error())
					}
					if pod.GetDeletionTimestamp() != nil {
						if err := RemovePodFinalizer(kube, pod); err != nil {
							fmt.Printf("Failed to remove finalizer: %s\n", err.Error())
						}
					}
				}
			}
		}
	}

}
