package main

import (
	"flag"

	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

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
	minAvailable        = 4
	containerImage      = "mattyiscool/experiment.base.th:1.2"
)

func main() {

	klog.InitFlags(nil)
	flag.Parse()
	klog.Info("starting dist jobs run")
	klog.Flush()

	sleep := 0
	for sleep < 10 {
		time.Sleep(1 * time.Second)
		klog.Infof("sleep %d", sleep)
		sleep++
	}

	for i := 0; i < len(cnnModels); i++ {
		klog.Infof("Starting %d/%d: %s in CNN", i+1, len(cnnModels), cnnModels[i])
		// TODO:
		// 1. create the volcano job object with gpu requested. and wait for each one to complete.
		// 2. Record time for each job, distribute level
	}

	for i := 0; i < len(langTasks); i++ {
		for j := 0; j < len(langModels); j++ {
			klog.Infof("Starting %d/%d model: %s task %s in CNN", i+1, len(langTasks), langModels[j], langTasks[i])
		}
	}
}

func generateArgs(model string, isCNN bool, worldSize int, machine int) []string {
	results := []string{}

	return results
}

func generateTasks(model string, isCNN bool, generatedJobName string, worldSize int, machine int) []vkapi.TaskSpec {
	// each machine will have its own task spec
	// because it needs different args
	results := []vkapi.TaskSpec{}
	var volumeClaimName string
	if isCNN {
		volumeClaimName = "cifar10"
	} else {
		volumeClaimName = "language-data"
	}

	args := generateArgs(model, isCNN, worldSize, machine)

	for i := 0; i < machine; i++ {
		t := vkapi.TaskSpec{
			// each task only need 1 replica
			Replicas: 1,
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
					},
					Containers: []v1.Container{
						{
							Image:   containerImage,
							Command: []string{"/bin/bash", "-c"},
							Args:    args,
							// TODO:
							Resources: v1.ResourceRequirements{},
						},
					},
				},
			},
		}

		results = append(results, t)
	}

	return results
}

// we are always running 4 for now
// because we want to demonstrate the locality problem
// 1. { N1: 4 }
// 2. { N1: 2, N2: 2}
// 3. { N1: 1, N2: 1, N3: 2}
func generateJob(name string, isCNN bool, worldSize int, machine int) *vkapi.Job {

	generatedJobName := name + RandStringBytes(2)
	taskSpecs := generateTasks(name, isCNN, generatedJobName, worldSize, machine)
	volcanoplugins := map[string][]string{}
	volcanoplugins["env"] = []string{}
	volcanoplugins["svc"] = []string{}
	return &vkapi.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      generatedJobName,
			Namespace: experimentNamespace,
		},
		Spec: vkapi.JobSpec{
			MinAvailable:  int32(minAvailable),
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
