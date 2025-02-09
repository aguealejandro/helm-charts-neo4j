package aws

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
	"github.com/neo4j/helm-charts/neo4j-admin/backup/common"
	"log"
	"os"
	"strings"
)

type resolverV2 struct{}

func (*resolverV2) ResolveEndpoint(ctx context.Context, params s3.EndpointParameters) (smithyendpoints.Endpoint, error) {
	// fallback to default
	return s3.NewDefaultEndpointResolverV2().ResolveEndpoint(ctx, params)
}

// CheckBucketAccess checks if the given bucket name is accessible or not
func (a *awsClient) CheckBucketAccess(bucketName string) error {

	client := a.getS3Client()
	//Create an Amazon S3 service client
	s3Input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	}
	if strings.Contains(bucketName, "/") {
		firstIndex := strings.Index(bucketName, "/")
		name := bucketName[:firstIndex]
		prefix := bucketName[firstIndex+1:]
		log.Printf("Name = %s , Prefix = %s", name, prefix)
		s3Input = &s3.ListObjectsV2Input{
			Bucket: aws.String(name),
			Prefix: aws.String(prefix),
		}
	}

	// Get the first page of results for ListObjectsV2 for a bucket
	objects, err := client.ListObjectsV2(context.TODO(), s3Input)
	if err != nil {
		return fmt.Errorf("Unable to connect to s3 bucket %s \n Here's why: %v\n", bucketName, err)
	}
	if strings.Contains(bucketName, "/") {
		if len(objects.Contents) == 0 {
			return fmt.Errorf("s3 Bucket %s does not exist", bucketName)
		}
	}
	log.Printf("Connectivity with S3 Bucket '%s' established", bucketName)

	return nil
}

// UploadFile uploads the file present at the provided location to the s3 bucket
func (a *awsClient) UploadFile(fileNames []string, bucketName string) error {

	s3Client := a.getS3Client()
	parentBucketName := bucketName
	// if bucketName is demo/test/test2
	// parentBucketName will be "demo"
	if strings.Contains(bucketName, "/") {
		index := strings.Index(bucketName, "/")
		parentBucketName = bucketName[:index]
	}
	//location := "/backups"
	location := os.Getenv("LOCATION")
	for _, fileName := range fileNames {

		filePath := fmt.Sprintf("%s/%s", location, fileName)
		yes, err := common.IsFileBigger(filePath)
		if err != nil {
			return err
		}
		//use UploadLargeObject if file size is more than 1GB
		if yes {
			err = a.UploadLargeObject(fileName, location, bucketName, parentBucketName)
			if err != nil {
				return err
			}
			continue
		}

		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("Couldn't open file %v to upload. Here's why: %v\n", filePath, err)
		}

		log.Printf("Starting upload of file %s", filePath)
		log.Printf("KeyName := %s", generateKeyName(bucketName, fileName))
		_, err = s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket: aws.String(parentBucketName),
			Key:    aws.String(generateKeyName(bucketName, fileName)),
			Body:   file,
		})
		if err != nil {
			return fmt.Errorf("Couldn't upload file %v to %v:%v. Here's why: %v\n", filePath, bucketName, fileName, err)
		}
		file.Close()
		log.Printf("File %s uploaded to s3 bucket %s !!", fileName, bucketName)
	}
	return nil
}

func (a *awsClient) UploadLargeObject(fileName string, location string, bucketName string, parentBucketName string) error {
	filePath := fmt.Sprintf("%s/%s", location, fileName)

	//divide the file into 1GB parts
	var partGiBs int64 = 1
	s3Client := a.getS3Client()
	uploader := manager.NewUploader(s3Client, func(u *manager.Uploader) {
		u.PartSize = partGiBs * 1024 * 1024 * 1024
	})

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("Couldn't open large file %v to upload. Here's why: %v\n", filePath, err)
	}

	defer file.Close()

	log.Printf("Starting upload of file %s", filePath)
	log.Printf("KeyName := %s", generateKeyName(bucketName, fileName))
	_, err = uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(parentBucketName),
		Key:    aws.String(generateKeyName(bucketName, fileName)),
		Body:   file,
	})
	if err != nil {
		return fmt.Errorf("Couldn't upload large file %v to %v:%v. Here's why: %v\n", filePath, bucketName, fileName, err)
	}
	log.Printf("File (Large) %s uploaded to s3 bucket %s !!", fileName, bucketName)
	return err
}

// GenerateEnvVariablesFromCredentials sets AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY
// This is required in the case when aggregate backup is to be performed but service account (role based creds) is not used
func (a *awsClient) GenerateEnvVariablesFromCredentials() error {
	creds, err := a.cfg.Credentials.Retrieve(context.TODO())
	if err != nil {
		return err
	}
	err = os.Setenv("AWS_ACCESS_KEY_ID", creds.AccessKeyID)
	if err != nil {
		return err
	}
	err = os.Setenv("AWS_SECRET_ACCESS_KEY", creds.SecretAccessKey)
	if err != nil {
		return err
	}
	err = os.Setenv("AWS_REGION", a.cfg.Region)
	if err != nil {
		return err
	}
	return nil
}

func generateKeyName(bucketName string, fileName string) string {
	keyName := fileName
	// if bucketName is demo/test/test2 , fileName is demo.backup
	// keyName should be test/test2/demo.backup
	if strings.Contains(bucketName, "/") {
		index := strings.Index(bucketName, "/")
		keyName = fmt.Sprintf("%s/%s", bucketName[index+1:], fileName)
	}
	return keyName
}

func (a *awsClient) getS3Client() *s3.Client {
	client := s3.NewFromConfig(*a.cfg)
	// if minio endpoint is provided add the endpoint resolver
	if value := os.Getenv("ENDPOINT"); strings.TrimSpace(value) != "" {
		client = s3.NewFromConfig(*a.cfg, func(options *s3.Options) {
			options.BaseEndpoint = aws.String(value)
			options.EndpointResolverV2 = &resolverV2{}
			options.UsePathStyle = true
		})
	}
	return client
}
