package cluster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
)

const (
	containerDir      = "/host/opt/bin"
	hostDir           = "/opt/bin"
	binaryName        = "controller"
	preloadBinaryName = "preload-binary"
)

// LoadControllerToClusterNodes loads the controller binary to all control plane nodes in the cluster.
// It is not intended to persist, just used for development purposes.
// The namespace and pod used are per the cluster configuration.
func (c *Cluster) LoadControllerToClusterNodes(ctx context.Context, infile io.ReadCloser) error {
	// deploy the daemonset
	labelKey := "app"
	labelValue := "preload-binary"
	labels := map[string]string{labelKey: labelValue}
	if err := deployPreloadBinaryDaemonSet(c.clientset, c.namespace, preloadBinaryName, labels); err != nil {
		return fmt.Errorf("deploying preload-binary DaemonSet: %w", err)
	}
	if err := copyToAllDaemonSetPods(c.clientset, c.apiConfig.Config, c.namespace, fmt.Sprintf("%s=%s", labelKey, labelValue), "writer", filepath.Join(containerDir, binaryName), infile); err != nil {
		return fmt.Errorf("copying to all DaemonSet pods: %w", err)
	}
	if err := removePreloadBinaryDaemonSet(c.clientset, c.namespace, preloadBinaryName); err != nil {
		return fmt.Errorf("removing preload-binary DaemonSet: %w", err)
	}
	// update our OCI image to point to the new image
	c.ociImage = devModeOCIImage

	// set our "running locally" flagOne la

	return nil
}

func copyToAllDaemonSetPods(
	clientset *kubernetes.Clientset,
	config *rest.Config,
	namespace, labelSelector, containerName, remotePath string,
	infile io.ReadCloser,
) error {
	ctx := context.TODO()

	podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return fmt.Errorf("no pods found with label selector: %s", labelSelector)
	}

	for _, pod := range podList.Items {
		fmt.Printf("Copying to pod: %s\n", pod.Name)
		err := streamToPod(clientset, config, namespace, pod.Name, containerName, remotePath, infile)
		if err != nil {
			return fmt.Errorf("copying to pod %s: %w", pod.Name, err)
		}
	}

	return nil
}

func streamToPod(
	clientset *kubernetes.Clientset,
	config *rest.Config,
	namespace, pod, container, remotePath string, infile io.ReadCloser,
) error {
	var parameterCodec = runtime.NewParameterCodec(
		scheme.Scheme,
	)
	req := clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   []string{"sh", "-c", fmt.Sprintf("cat > %s && chmod +x %s", remotePath, remotePath)},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
		}, parameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("creating executor: %w", err)
	}

	var (
		stdout, stderr bytes.Buffer
		ctx            = context.TODO()
	)
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  infile,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return fmt.Errorf("stream error: %w\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	return nil
}

func deployPreloadBinaryDaemonSet(clientset *kubernetes.Clientset, namespace, name string, labels map[string]string) error {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "node-role.kubernetes.io/control-plane",
												Operator: corev1.NodeSelectorOpExists,
											},
										},
									},
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "node-role.kubernetes.io/master",
												Operator: corev1.NodeSelectorOpExists,
											},
										},
									},
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "writer",
							Image:   utilityImageName,
							Command: []string{"sh", "-c", "sleep 3600"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "host-bin",
									MountPath: containerDir,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "host-bin",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: hostDir,
									Type: newHostPathType(corev1.HostPathDirectoryOrCreate),
								},
							},
						},
					},
				},
			},
		},
	}

	ctx := context.TODO()
	_, err := clientset.AppsV1().DaemonSets(namespace).Create(ctx, ds, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create DaemonSet: %w", err)
	}

	// wait for the DaemonSet to be ready
	for {
		time.Sleep(2 * time.Second)

		dsStatus, err := clientset.AppsV1().DaemonSets(namespace).Get(ctx, ds.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("error getting DaemonSet status: %w", err)
		}

		desired := dsStatus.Status.DesiredNumberScheduled
		ready := dsStatus.Status.NumberReady

		fmt.Printf("Pods ready: %d / %d\n", ready, desired)

		if desired > 0 && ready == desired {
			fmt.Println("All DaemonSet pods are ready.")
			break
		}
	}

	return nil
}

func removePreloadBinaryDaemonSet(clientset *kubernetes.Clientset, namespace, name string) error {
	ctx := context.TODO()
	return clientset.AppsV1().DaemonSets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func newHostPathType(t corev1.HostPathType) *corev1.HostPathType {
	return &t
}
