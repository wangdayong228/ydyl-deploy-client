package deploy

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"
)

const maxRunInstancesBatchSize = 50

type EC2RunInstancesLauncher struct {
	ctx               context.Context
	ec2Client         *ec2.EC2
	commonCfg         CommonConfig
	buildInstanceName func(tagPrefix, serviceType string, ordinal int) string
}

func NewEC2RunInstancesLauncher(ctx context.Context, ec2Client *ec2.EC2, commonCfg CommonConfig, buildInstanceName func(tagPrefix, serviceType string, ordinal int) string) *EC2RunInstancesLauncher {
	return &EC2RunInstancesLauncher{
		ctx:               ctx,
		ec2Client:         ec2Client,
		commonCfg:         commonCfg,
		buildInstanceName: buildInstanceName,
	}
}

func (l *EC2RunInstancesLauncher) Run(svc ServiceConfig) ([]*string, error) {
	totalCount := int(svc.Count)
	if totalCount <= 0 {
		return nil, nil
	}

	totalBatches := (totalCount + maxRunInstancesBatchSize - 1) / maxRunInstancesBatchSize
	remaining := totalCount
	allIDs := make([]*string, 0, totalCount)
	for batchNo := 1; remaining > 0; batchNo++ {
		batchCount := remaining
		if batchCount > maxRunInstancesBatchSize {
			batchCount = maxRunInstancesBatchSize
		}
		log.Printf("🧩 [%s] 创建实例批次 %d/%d，计划创建 %d 台，候选机型=%v\n", svc.Type.String(), batchNo, totalBatches, batchCount, svc.InstanceType)

		ids, usedInstanceType, err := l.runInstancesBatchWithFallback(svc, batchCount, batchNo, totalBatches)
		if err != nil {
			return nil, err
		}
		if len(ids) != batchCount {
			return nil, fmt.Errorf("[%s] 批次 %d/%d 创建数量异常，期望=%d，实际=%d", svc.Type.String(), batchNo, totalBatches, batchCount, len(ids))
		}

		// 为每台实例按服务维度的全局序号打 Name 标签，保持与原有命名规则一致。
		if err := l.tagInstancesSequentially(svc, ids, len(allIDs)+1); err != nil {
			return nil, err
		}
		log.Printf("✅ [%s] 批次 %d/%d 创建成功，机型=%s，实例数=%d\n", svc.Type.String(), batchNo, totalBatches, usedInstanceType, len(ids))

		allIDs = append(allIDs, ids...)
		remaining -= len(ids)
	}

	return allIDs, nil
}

func (l *EC2RunInstancesLauncher) runInstancesBatchWithFallback(svc ServiceConfig, batchCount int, batchNo int, totalBatches int) ([]*string, string, error) {
	for idx, instanceType := range svc.InstanceType {
		log.Printf("🚀 [%s] 批次 %d/%d 尝试机型(%d/%d): %s\n", svc.Type.String(), batchNo, totalBatches, idx+1, len(svc.InstanceType), instanceType)

		input := l.buildRunInstancesInput(svc, instanceType, batchCount)
		out, err := l.ec2Client.RunInstancesWithContext(l.ctx, input)
		if err == nil {
			ids := make([]*string, 0, len(out.Instances))
			for _, inst := range out.Instances {
				ids = append(ids, inst.InstanceId)
			}
			return ids, instanceType, nil
		}

		if isCapacityError(err) {
			log.Printf("⚠️ [%s] 批次 %d/%d 机型 %s 容量不足，尝试下一个机型，err=%v\n", svc.Type.String(), batchNo, totalBatches, instanceType, err)
			continue
		}
		return nil, "", fmt.Errorf("[%s] 批次 %d/%d 启动实例失败（机型=%s）: %w", svc.Type.String(), batchNo, totalBatches, instanceType, err)
	}

	return nil, "", fmt.Errorf("[%s] 批次 %d/%d 所有机型均容量不足: %s", svc.Type.String(), batchNo, totalBatches, strings.Join(svc.InstanceType, ","))
}

func (l *EC2RunInstancesLauncher) buildRunInstancesInput(svc ServiceConfig, instanceType string, count int) *ec2.RunInstancesInput {
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(svc.AMI),
		InstanceType: aws.String(instanceType),
		MinCount:     aws.Int64(int64(count)),
		MaxCount:     aws.Int64(int64(count)),
		KeyName:      aws.String(l.commonCfg.KeyName),
		SecurityGroupIds: []*string{
			aws.String(l.commonCfg.SecurityGroupID),
		},
		InstanceInitiatedShutdownBehavior: aws.String("terminate"),
		TagSpecifications:                 []*ec2.TagSpecification{},
	}
	if l.commonCfg.DiskSizeGiB > 0 {
		input.BlockDeviceMappings = []*ec2.BlockDeviceMapping{
			{
				DeviceName: aws.String("/dev/sda1"),
				Ebs: &ec2.EbsBlockDevice{
					VolumeSize:          aws.Int64(l.commonCfg.DiskSizeGiB),
					VolumeType:          aws.String("gp3"),
					DeleteOnTermination: aws.Bool(true),
				},
			},
		}
	}
	return input
}

func (l *EC2RunInstancesLauncher) tagInstancesSequentially(svc ServiceConfig, ids []*string, startOrdinal int) error {
	for i, id := range ids {
		name := l.buildInstanceName(svc.TagPrefix, svc.Type.String(), startOrdinal+i)
		_, err := l.ec2Client.CreateTagsWithContext(l.ctx, &ec2.CreateTagsInput{
			Resources: []*string{id},
			Tags: []*ec2.Tag{
				{
					Key:   aws.String("Name"),
					Value: aws.String(name),
				},
			},
		})
		if err != nil {
			return fmt.Errorf("为实例 %s 打标签失败: %w", aws.StringValue(id), err)
		}
	}
	return nil
}

func isCapacityError(err error) bool {
	if err == nil {
		return false
	}
	if awsErr, ok := err.(awserr.Error); ok {
		switch awsErr.Code() {
		case "InsufficientInstanceCapacity",
			"InsufficientReservedInstanceCapacity",
			"InsufficientFreeAddressesInSubnet":
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "insufficient") && strings.Contains(msg, "capacity")
}
