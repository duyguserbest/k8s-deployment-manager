package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/emicklei/go-restful"
	"io"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/client-go/util/retry"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"

	//Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

var clientset *kubernetes.Clientset
var deployment *appsv1.Deployment
var err error

type DeploymentInfo struct {
	Image, Namespace string
}

func main() {
	CreateClient()
	CreateDeploymentStruct()
	CreateWebService()
}

func CreateWebService() {
	ws := new(restful.WebService)
	ws.Path("/deployment").
		Consumes(restful.MIME_JSON, restful.MIME_JSON).
		Produces(restful.MIME_JSON, restful.MIME_JSON)
	ws.Route(ws.GET("/namespace/{namespace-name}").To(ListDeployment))
	ws.Route(ws.POST("").To(CreateDeployment))
	ws.Route(ws.PATCH("/{deployment-name}/namespace/{namespace-name}").To(UpdateDeployment))
	ws.Route(ws.DELETE("/{deployment-name}/namespace/{namespace-name}").To(DeleteDeployment))
	restful.Add(ws)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func CreateDeploymentStruct() {
	deployment = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "demo-deployment",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "demo",
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "demo",
					},
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:  "web",
							Image: "nginx:1.12",
							Ports: []apiv1.ContainerPort{
								{
									Name:          "http",
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: 80,
								},
							},
						},
					},
				},
			},
		},
	}
}

func CreateClient() {
	config, err1 := rest.InClusterConfig()
	if err1 != nil {
		kubeconfig := ReadKubeConfig()
		config = BuildConfigFromKubeConfig(config, kubeconfig)
	}
	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal("Failed to create k8s api client.", err)
	}
}

func BuildConfigFromKubeConfig(config *rest.Config, kubeconfig *string) *rest.Config {
	config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		log.Fatal("Failed to configure client. Must run in cluster with a service account or must have a available config file on directory ~/.kube/")
	}
	return config
}

func ReadKubeConfig() *string {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()
	return kubeconfig
}

func CreateDeployment(req *restful.Request, resp *restful.Response) {
	deploy := new(DeploymentInfo)
	req.ReadEntity(&deploy)
	fmt.Println("Creating deployment...")
	appName := RemoveNonAlphanumericChars(deploy.Image)
	deployment.ObjectMeta.Name = appName
	deployment.Spec.Selector.MatchLabels["app"] = appName
	deployment.Namespace = deploy.Namespace
	deployment.Spec.Template.ObjectMeta.Labels["app"] = appName
	deployment.Spec.Template.Spec.Containers[0].Image = deploy.Image

	err := CreateNamespace(deploy)

	deploymentsClient := clientset.AppsV1().Deployments(deploy.Namespace)
	result, err := deploymentsClient.Create(deployment)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Created deployment %q.\n", result.GetObjectMeta().GetName())
	io.WriteString(resp, "Created deployment "+result.GetObjectMeta().GetName())
}

func CreateNamespace(deploy *DeploymentInfo) error {
	nsSpec := &apiv1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: deploy.Namespace}}
	_, err := clientset.Core().Namespaces().Create(nsSpec)
	return err
}

func UpdateDeployment(req *restful.Request, resp *restful.Response) {
	namespace := req.PathParameter("namespace-name")
	deploymentName := req.PathParameter("deployment-name")
	fmt.Println("Updating deployment...")
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploymentsClient := clientset.AppsV1().Deployments(namespace)
		result, getErr := deploymentsClient.Get(deploymentName, metav1.GetOptions{})
		if getErr != nil {
			panic(fmt.Errorf("Failed to get latest version of Deployment: %v", getErr))
		}

		result.Spec.Replicas = int32Ptr(1)                           // reduce replica count
		result.Spec.Template.Spec.Containers[0].Image = "nginx:1.13" // change nginx version
		_, updateErr := deploymentsClient.Update(result)
		return updateErr
	})
	if retryErr != nil {
		panic(fmt.Errorf("Update failed: %v", retryErr))
	}
	fmt.Println("Updated deployment...")
	io.WriteString(resp, "Updated deployment...")
}

func ListDeployment(req *restful.Request, resp *restful.Response) {
	namespace := req.PathParameter("namespace-name")
	fmt.Printf("Listing deployments in namespace %q:\n", namespace)
	deploymentsClient := clientset.AppsV1().Deployments(namespace)
	list, err := deploymentsClient.List(metav1.ListOptions{})
	if err != nil {
		panic(err)
	}
	var buffer bytes.Buffer
	for _, d := range list.Items {
		buffer.WriteString(d.Name)
		buffer.WriteString(" ")
		buffer.WriteString(strconv.FormatInt(int64(*d.Spec.Replicas), 10))
		buffer.WriteString("\n")
		fmt.Printf(" * %s (%d replicas)\n", d.Name, *d.Spec.Replicas)
	}
	io.WriteString(resp, buffer.String())
}

func DeleteDeployment(req *restful.Request, resp *restful.Response) {
	namespace := req.PathParameter("namespace-name")
	deploymentName := req.PathParameter("deployment-name")
	fmt.Println("Deleting deployment...")
	deletePolicy := metav1.DeletePropagationForeground
	deploymentsClient := clientset.AppsV1().Deployments(namespace)
	if err := deploymentsClient.Delete(deploymentName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		fmt.Println(err.Error())
		resp.WriteError(500, err)
	}
	fmt.Println("Deleted deployment.")
	io.WriteString(resp, "Deleted deployment.")
}

func int32Ptr(i int32) *int32 { return &i }

func RemoveNonAlphanumericChars(value string) string {
	reg, err := regexp.Compile("[^a-zA-Z0-9]+")
	if err != nil {
		log.Fatal(err)
	}
	return reg.ReplaceAllString(value, "")
}
