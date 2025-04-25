package scheduler

import (
    "context"
    "math"
	"fmt"
	"time"
    "os"
    "log"
    "path/filepath"
    "k8s.io/client-go/tools/clientcmd"
	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
    "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)


type WorkerNode struct {
    Name string
    IPAddress string
}

func QueryPrometheus(address string, query string) (float64, error) {
    addr := "http://" + address + ":9090"
    client, err := api.NewClient(api.Config{
		Address: addr,
	})
	if err != nil {
		return 0, fmt.Errorf("creating prometheus client: %w", err)
	}

	v1api := promv1.NewAPI(client)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, warnings, err := v1api.Query(ctx, query, time.Now())
	if err != nil {
		return 0, fmt.Errorf("query error: %w", err)
	}
	if len(warnings) > 0 {
		fmt.Printf("Prometheus warnings: %v\n", warnings)
	}

	vector, ok := result.(model.Vector)
	if !ok || len(vector) == 0 {
		return 0, fmt.Errorf("no result or unexpected format")
	}

	raw := float64(vector[0].Value)
	rounded := math.Round(raw*100) / 100

	return rounded, nil
}

func GetBestNode(address string) WorkerNode {
    nodes, err := GetWorkers()

    if err != nil {
        log.Printf("Could not retrieve worker nodes: %s", err)
    }

    w1 := 0.1  // TX Drop 
    w2 := 0.2  // RX Error
    w3 := 0.5  // Total Traffic
    w4 := 0.2  // OvS Util

    bestScore := -1.1
    var bestNode WorkerNode
    for _, node := range nodes {
        t_instance := node.IPAddress + ":9100"
        cpuQuery := fmt.Sprintf(`100 - (avg by(instance) (rate(node_cpu_seconds_total{instance="%s", mode="idle"}[5m])) * 100)`, t_instance)
        value, err := QueryPrometheus(address, cpuQuery)
        if err != nil {
            log.Printf("error occurred, could not get CPU: %v", err)
        }

        fmt.Println(value)
        if value > 95 {
            log.Printf("%s CPU too high. Unable to bind pod to node.", node.Name)
            continue
        }

        memQuery := fmt.Sprintf(`(node_memory_MemTotal_bytes{instance="%s"} - node_memory_MemAvailable_bytes{instance="%s"}) / node_memory_MemTotal_bytes{instance="%s"}`, t_instance, t_instance, t_instance)
        value, err = QueryPrometheus(address, memQuery)
        if err != nil {
            log.Printf("Prometheus error while fetching memory: %v", err)
        }
        if value > 0.95 {
            log.Printf("%s memory too high. Unable to bind pod to node.", node.Name)
            continue
        }
        // connecting to my ovs exporter
        instance := node.IPAddress + ":9101"
        txDropQuery := fmt.Sprintf(`sum(rate(ovs_interface_tx_dropped_total{instance="%s"}[5m])) /sum(rate(ovs_interface_tx_packets_total{instance="%s"}[5m]))`, instance, instance)
        txDrop, err := QueryPrometheus(address, txDropQuery)
        if err != nil {
            log.Printf("Prometheus error while fetching OvS metric: %v", err)
        }


        rxErrQuery := fmt.Sprintf(`sum(rate(ovs_interface_rx_errors_total{instance="%s"}[5m])) / sum(rate(ovs_interface_rx_packets_total{instance="%s"}[5m]))`, instance, instance)
        rxErr, err := QueryPrometheus(address, rxErrQuery)
        if err != nil {
            log.Printf("Prometheus error while fetching OvS metric: %v", err)
        }

        ovsUtilQuery := fmt.Sprintf(`100 * (sum(rate(ovs_interface_tx_bytes_total{instance="%s"}[5m])) + sum(rate(ovs_interface_rx_bytes_total{instance="%s"}[5m]))) / 125000000`, instance, instance)

        ovsUtil, err := QueryPrometheus(address, ovsUtilQuery)
        if err != nil {
            log.Printf("Prometheus error while fetching OvS metric: %v", err)
        }

        totalTrafficQuery := fmt.Sprintf(`rate(node_network_transmit_bytes_total{instance="%s"}[5m]) + rate(node_network_receive_bytes_total{instance="%s"}[5m])`, t_instance, t_instance)
        totalTraffic, err := QueryPrometheus(address, totalTrafficQuery)
        if err != nil {
            log.Printf("Prometheus error while fetching OvS metric: %v", err)
        }

        maxTraffic := 125000000.0
        normalizedTraffic := 1 - (totalTraffic / maxTraffic)
        normalizedUtil := 1 - (ovsUtil / 100)

        if normalizedTraffic < 0 {
	        normalizedTraffic = 0
        }
        if normalizedUtil < 0 {
	        normalizedUtil = 0
        }

        log.Printf("[%s] Metrics - txDrop: %.4f, rxErr: %.4f, ovsUtil: %.2f%%, totalTraffic: %.2fMB/s", node.Name, txDrop, rxErr, ovsUtil*100, (totalTraffic/(1024*1024))*100)


        // Final score calculation
        score := w1*(1 - txDrop) + w2*(1 - rxErr) + w3*normalizedTraffic + w4*normalizedUtil
        if score > bestScore {
            bestScore = score
            bestNode = node
        }
    }

    log.Printf("\nBest node for VNF: %s (score: %.4f)\n", bestNode.Name, bestScore)

    return bestNode


}

func GetWorkers() ([]WorkerNode, error) {
    config, err := loadKubeConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	nodeList, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var workers []WorkerNode
	for _, node := range nodeList.Items {
		// Skip control-plane nodes
		if _, isControlPlane := node.Labels["node-role.kubernetes.io/control-plane"]; isControlPlane {
			continue
		}

		var nodeIP string
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" {
				nodeIP = addr.Address
				break
			}
		}

		workers = append(workers, WorkerNode{
			Name: node.Name,
			IPAddress:   nodeIP,
		})
	}

	return workers, nil
}



func loadKubeConfig() (*rest.Config, error) {
	// Try in-cluster config
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	// Fall back to local auth
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		kubeconfig = "/etc/kubernetes/admin.conf"
	}

	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

