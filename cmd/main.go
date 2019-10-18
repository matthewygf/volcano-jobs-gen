package main

import (
	"encoding/csv"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	resourcehelper "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"volcano.sh/volcano/pkg/client/clientset/versioned"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	// "sigs.k8s.io/yaml"

	vkapi "volcano.sh/volcano/pkg/apis/batch/v1alpha1"
)

var (
	cnnModels = []string{
		"googlenet",
		"squeezenet1_0",
		"mobilenet",
		"mobilenet_large",
		"shufflenetv2_0_5",
		"shufflenetv2_1_0",
		"resnet18",
		"resnet34",
		"resnet50",
		"densenet121",
		"densenet161",
		"densenet169",
		"vgg11",
		"vgg19",
		"mnasnet0_5",
		"mnasnet1_0",
		"dpn26_small",
		"pyramidnet_48_110",
	}

	langModels = []string{
		"lstm",
		"transformer",
	}

	langTasks = []string{
		"lm",
		"mt",
	}

	jobName             = "job.volcano.sh"
	scheduleName        = "volcano"
	experimentNamespace = "default"
	containerImage      = "mattyiscool/experiment.base.th:1.2"
	incluster           = flag.Bool("incluster", false, "whether this app is launch within a cluster, will indicate to build clientset using rbac configs")
	kubeconfig          = flag.String("kubeconfig", "", "path to kubeconfig to initialize clientset for querying api")
	apiserverurl        = flag.String("apiserverurl", "", "url to apiserver for kube client config")

	k8Client *kubernetes.Clientset
	vkClient *versioned.Clientset

	startFromMachine = flag.Int("start_machine", 2, "generate jobs for the number of machines starting from this.")
	endFromMachine   = flag.Int("end_machine", 3, "generate jobs for the number of machines ending on this number. must be >= start_machine.")
	totalWorldSize   = flag.Int("world_size", 6, "total world size , i.e. total number of the processes")
	runCNNOnly       = flag.Bool("run_cnn_only", false, "only generate and run for cnn")
	runNLPOnly       = flag.Bool("run_nlp_only", false, "only generate and run for nlp models")
)

func initClient() (*rest.Config, error) {
	if *incluster {
		config, err := rest.InClusterConfig()
		if err != nil {
			klog.Exitln(err)
		}
		return config, nil
	}

	var defaultKube = "KUBECONFIG"
	if *kubeconfig == "" {
		if os.Getenv(defaultKube) != "" {
			*kubeconfig = os.Getenv(defaultKube)
		}
	}

	config, err := clientcmd.BuildConfigFromFlags(*apiserverurl, *kubeconfig)
	if err != nil {
		klog.Exitln("No master url provided or kube config provided")
	}

	return config, nil
}

func main() {

	klog.InitFlags(nil)
	flag.Parse()
	klog.Info("starting dist jobs run")
	klog.Flush()

	config, err := initClient()
	if err != nil {
		klog.Fatalf("Failed to init client %v", err)
	}

	k8Client = kubernetes.NewForConfigOrDie(config)
	vkClient = versioned.NewForConfigOrDie(config)

	if !*runNLPOnly {
		for i := 0; i < len(cnnModels); i++ {
			klog.Infof("Starting %d/%d: %s in CNN", i+1, len(cnnModels), cnnModels[i])
			// 1. create the pvc for the volcano job
			// 2. create the volcano job object with gpu requested. and wait for each one to complete.
			// 3. Record time for each job, distribute level
			for j := *startFromMachine; j <= *endFromMachine; j++ {
				vkjob := generateJob(cnnModels[i], "cifar10", true, *totalWorldSize, j)

				if vkjob == nil {
					klog.Errorf("Could not gen job for %v", cnnModels[i])
				} else {
					err := generateAndCreatePVC(vkjob.Name)
					if err != nil && !apierrors.IsAlreadyExists(err) {
						klog.Errorf("%v", err)
						continue
					}

					submitJob(vkjob)
				}
			}
		}
	}

	if !*runCNNOnly {
		for i := 0; i < len(langTasks); i++ {
			for j := 0; j < len(langModels); j++ {
				klog.Infof("Starting %d/%d model: %s task %s in CNN", i+1, len(langTasks), langModels[j], langTasks[i])

				for m := *startFromMachine; m <= *endFromMachine; m++ {
					vkjob := generateJob(langModels[j], langTasks[i], false, *totalWorldSize, m)

					if vkjob == nil {
						klog.Errorf("Could not gen job for %v", cnnModels[i])
					} else {
						err := generateAndCreatePVC(vkjob.Name)
						if err != nil && !apierrors.IsAlreadyExists(err) {
							klog.Errorf("%v", err)
							continue
						}
						submitJob(vkjob)
					}
				}
			}
		}
	}

	var fileHandle *os.File
	var writer *csv.Writer
	// TODO: Make this as a path to a flag defined dir.
	fileName := "jct_dist.csv"
	secInUnix := time.Now().Unix()
	path := filepath.Join("/mnt/csvs", strconv.FormatInt(secInUnix, 10))
	pathFileName := filepath.Join(path, fileName)
	if _, err := os.Stat(pathFileName); os.IsNotExist(err) {
		err := os.MkdirAll(path, os.FileMode(0777))
		fileHandle, err = os.Create(pathFileName)
		fileHandle.Chmod(os.FileMode(0777))
		writer = csv.NewWriter(fileHandle)
		header := []string{"job_name", "creation_time", "last_transition_time"}
		err = writer.Write(header)
		if err != nil {
			klog.Fatalf("%v", err)
		}
		writer.Flush()
	} else {
		fileHandle, err = os.OpenFile(pathFileName, os.O_RDWR|os.O_APPEND, os.FileMode(0777))
		if err != nil {
			klog.Errorf("Cannot open the file. Experiment should be restart")
		}
		writer = csv.NewWriter(fileHandle)
	}

	defer func() {
		if err := fileHandle.Close(); err != nil {
			klog.Fatalf("%v\n", err)
		}
	}()

	jobs, err := vkClient.BatchV1alpha1().Jobs(experimentNamespace).List(metav1.ListOptions{})
	if err != nil {
		klog.Fatalf("%v\n", err)
	}

	for _, j := range jobs.Items {
		err = writer.Write([]string{j.CreationTimestamp.UTC().String(), j.Status.State.LastTransitionTime.UTC().String()})
		if err != nil {
			klog.Errorf("*********** error recording jct for job %s %v\n", j.Name, err)
		}
		writer.Flush()
	}
	time.Sleep(2)
	klog.Flush()
}

func submitJob(vkjob *vkapi.Job) {
	klog.Infof("submitting job %s \n", vkjob.Name)
	_, err := vkClient.BatchV1alpha1().Jobs(experimentNamespace).Create(vkjob)
	if err != nil {
		klog.Fatalf("%v", err)
	} else {
		// check until this job is finish before starting another.
		wait := true
		for wait {
			time.Sleep(5 * time.Second)
			j, err := vkClient.BatchV1alpha1().Jobs(experimentNamespace).Get(vkjob.Name, metav1.GetOptions{})
			if err != nil {
				klog.Errorf("%v", err)
			} else {
				if j.Status.State.Phase == vkapi.Completed {
					wait = false
				} else {
					klog.Infof("Job %v still running: %v", vkjob.Name, j.Status.State.Phase)
				}
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func generateArgs(model string, dataset string, taskName string, jobName string, isCNN bool, worldSize int) string {
	var builder strings.Builder

	builder.WriteString("git clone https://github.com/matthewygf/torch_interference.git && cd torch_interference && python ")
	if isCNN {
		builder.WriteString("image_classifier.py ")
	} else {
		builder.WriteString("languages.py ")
	}

	builder.WriteString("--model=" + model)
	unixtime := strconv.Itoa(time.Now().UTC().Second())
	builder.WriteString(" --run_name=" + unixtime)
	builder.WriteString(" --dataset_dir=/mnt/data")
	builder.WriteString(" --dataset=" + dataset)
	builder.WriteString(" --ckpt_dir=/mnt/ckpt")

	if !isCNN {
		builder.WriteString(" --embeddings_dim=64 --hiddens_dim=128 --max_vocabs=10000 --max_sentence_length=250")
		if strings.Contains(model, "lstm") {
			builder.WriteString(" --bidirectional")
		}
		builder.WriteString(" --batch_size=16")
		builder.WriteString(" --task=" + taskName)
	} else {
		builder.WriteString(" --batch_size=128")
	}

	// the distributed part.
	builder.WriteString(" --discover_gpus")
	builder.WriteString(" --world_size=" + strconv.Itoa(worldSize))
	builder.WriteString(" --dist_backend=nccl")
	// we always know that the tcp method only need to point to rank0, and in our case, its master 0
	builder.WriteString(" --dist_method=tcp://" + jobName + "-master-0." + jobName + ":" + strconv.Itoa(2222))

	return builder.String()
}

func generateTasks(model string, dataset string, isCNN bool, modelTaskName string, generatedJobName string, ckptClaimName string, worldSize int, machine int) []vkapi.TaskSpec {
	// each machine will have its own task spec
	// because it needs different args
	results := []vkapi.TaskSpec{}
	var volumeClaimName string
	if isCNN {
		volumeClaimName = "cifar10"
	} else {
		volumeClaimName = "language-data"
	}

	taskName := ""
	args := generateArgs(model, dataset, modelTaskName, generatedJobName, isCNN, worldSize)

	gpuPerTask := int(math.Floor(float64(worldSize) / float64(machine)))
	remainGPU := worldSize - (gpuPerTask * machine)

	assign := gpuPerTask
	for i := 0; i < machine; i++ {

		containerArgs := args + " --rank=" + strconv.Itoa(i)

		if i == 0 {
			taskName = "master"
		} else {
			taskName = "worker" + strconv.Itoa(i)
		}

		if i > int(machine-remainGPU)-1 {
			// if we have remainder, distribute the remains equally on machine, give it more gpu
			assign = gpuPerTask + 1
			containerArgs += " --assume_same_gpus=false"
		}
		hostName := "core-gpu-0" + strconv.Itoa(i+1)
		resList := v1.ResourceList{
			v1.ResourceName("nvidia.com/gpu"): resourcehelper.MustParse(strconv.Itoa(assign)),
		}

		devices := []string{}
		for d := 0; d < assign; d++ {
			devices = append(devices, strconv.Itoa(d))
		}

		nodeselectors := map[string]string{}
		nodeselectors["kubernetes.io/hostname"] = hostName

		t := vkapi.TaskSpec{
			// each task only need 1 replica
			Replicas: 1,
			Name:     taskName,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:   generatedJobName,
					Labels: map[string]string{jobName: generatedJobName},
				},
				Spec: v1.PodSpec{
					RestartPolicy: v1.RestartPolicyNever,
					Volumes: []v1.Volume{
						{
							Name: "nfs-pvc-datasets",
							VolumeSource: v1.VolumeSource{
								PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
									ClaimName: volumeClaimName,
								},
							},
						},
						{
							Name: "nfs-pvc-ckpt",
							VolumeSource: v1.VolumeSource{
								PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
									ClaimName: ckptClaimName,
								},
							},
						},
					},
					NodeSelector: nodeselectors,
					Containers: []v1.Container{
						{
							Name:    generatedJobName + "-" + strconv.Itoa(i),
							Image:   containerImage,
							Command: []string{"/bin/bash", "-c"},
							Args:    []string{containerArgs},
							Resources: v1.ResourceRequirements{
								Limits: resList,
							},
							Env: []v1.EnvVar{
								{
									Name:  "NVIDIA_VISIBLE_DEVICES",
									Value: strings.Join(devices, ","),
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "nfs-pvc-datasets",
									MountPath: "/mnt/data",
								},
								{
									Name:      "nfs-pvc-ckpt",
									MountPath: "/mnt/ckpt",
								},
							},
							Ports: []v1.ContainerPort{
								{
									Name:          "job-port",
									ContainerPort: 2222,
								},
							},
						},
					},
				},
			},
		}

		results = append(results, t)
	}

	return results
}

func generateAndCreatePVC(jobName string) error {
	annotation := map[string]string{}
	annotation[v1.BetaStorageClassAnnotation] = "managed-nfs-storage"
	modes := []v1.PersistentVolumeAccessMode{}
	modes = append(modes, "ReadWriteMany")
	resourceSize := "100Mi"
	if strings.Contains(jobName, "vgg") {
		resourceSize = "550Mi"
	}
	requestResList := v1.ResourceList{
		v1.ResourceName(v1.ResourceStorage): resourcehelper.MustParse(resourceSize),
	}

	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName + "ckpt",
			Annotations: annotation,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: modes,
			Resources: v1.ResourceRequirements{
				Requests: requestResList,
			},
		},
	}

	klog.Infof("%v\n", pvc)

	_, err := k8Client.CoreV1().PersistentVolumeClaims(experimentNamespace).Create(pvc)

	if err != nil {
		klog.Errorf("%v", err)
		return err
	}

	return nil
}

// we are always running 4 for now
// because we want to demonstrate the locality problem
// 1. { N1: 4 }
// 2. { N1: 2, N2: 2}
// 3. { N1: 1, N2: 1, N3: 2}
func generateJob(name string, modelTaskName string, isCNN bool, worldSize int, machine int) *vkapi.Job {

	generatedJobName := name + RandStringBytes(2) + "-machine" + strconv.Itoa(machine)
	datasetName := ""
	if isCNN {
		datasetName = "cifar10"
	} else {
		if modelTaskName == "lm" {
			datasetName = "wikitext"
		} else {
			datasetName = "nc_zhen"
		}
	}

	ckptClaimName := generatedJobName + "ckpt"

	taskSpecs := generateTasks(name, datasetName, isCNN, modelTaskName, generatedJobName, ckptClaimName, worldSize, machine)
	volcanoplugins := map[string][]string{}
	volcanoplugins["env"] = []string{}
	volcanoplugins["svc"] = []string{}
	return &vkapi.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generatedJobName,
			Namespace: experimentNamespace,
		},
		Spec: vkapi.JobSpec{
			MinAvailable:  int32(len(taskSpecs)),
			SchedulerName: scheduleName,
			Plugins:       volcanoplugins,
			Policies: []vkapi.LifecyclePolicy{
				{
					Action: vkapi.RestartJobAction,
					Event:  vkapi.PodEvictedEvent,
				},
			},
			Tasks: taskSpecs,
		},
	}
}
