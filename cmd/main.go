package main

import (
	"context"
	"fmt"
	"log"
	"vnf-scheduler/pkg/scheduler"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to get in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Watch for unscheduled pods with our custom scheduler name
	watcher, err := clientset.CoreV1().Pods("").Watch(context.TODO(), metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.schedulerName", "custom-vnf-scheduler").String(),
	})
	if err != nil {
		log.Fatalf("Failed to watch pods: %v", err)
	}

	fmt.Println("Custom VNF Scheduler started...")

	for event := range watcher.ResultChan() {
		pod, ok := event.Object.(*v1.Pod)
		if !ok || pod.Spec.NodeName != "" {
			continue
		}

		fmt.Printf("Scheduling pod: %s/%s\n", pod.Namespace, pod.Name)

		node := scheduler.GetBestNode("1.1.1.1")
		if node.Name == "" {
			log.Println("No suitable node found")
			continue
		}

		err = bindPod(clientset, pod, node.Name)
		if err != nil {
			log.Printf("Failed to bind pod: %v", err)
		} else {
			fmt.Printf("Bound pod %s/%s to node %s\n", pod.Namespace, pod.Name, node.Name)
		}
	}
}

func bindPod(clientset *kubernetes.Clientset, pod *v1.Pod, nodeName string) error {
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Target: v1.ObjectReference{
			Kind: "Node",
			Name: nodeName,
		},
	}
	return clientset.CoreV1().Pods(pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{})
}


