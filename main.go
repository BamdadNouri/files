package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/fvbock/endless"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func main() {
	run()
}

func run() {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = ioutil.Discard
	engine := gin.Default()
	config := cors.DefaultConfig()
	config.AllowOriginFunc = func(origin string) bool {
		return true
	}
	config.AllowCredentials = true
	config.AllowHeaders = []string{
		"Origin", "Content-Length", "Content-Type",
		"X-Screen-Height", "X-Screen-Width", "Authorization",
	}
	engine.Use(cors.New(config))
	c, _ := NewConfig()
	ctx := context.Background()

	minioClient := handleMinio(ctx, c)
	handler := NewHandler(minioClient, c)

	statics := engine.Group("/")
	engine.LoadHTMLGlob("./public/*.html")
	statics.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})
	statics.GET("/share", func(c *gin.Context) {
		c.HTML(http.StatusOK, "share.html", nil)
	})

	api := engine.Group("/api")
	api.POST("upload", handler.Upload)
	api.GET("link/:key", handler.GetLinks)

	endless.ListenAndServe(fmt.Sprintf("0.0.0.0:%s", c.Port), engine)
}

type Handler struct {
	minioClient *minio.Client
	config      *Config
}

func NewHandler(minioClient *minio.Client, config *Config) *Handler {
	return &Handler{minioClient, config}
}

func (h *Handler) Upload(c *gin.Context) {
	file, err := c.FormFile("file")
	k, _ := c.GetPostForm("key")
	fmt.Println(k)
	ctx := context.Background()
	// TODO add format
	objectName, err := handleUploads(ctx, h.minioClient, h.config.Minio.BucketName, h.config.SharingDirectoryPrefix+generateID(), file.Header.Get("content-type"), file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, "something went wrong")
		return
	}
	res := gin.H{"key": objectName}
	sharing, _ := c.GetQuery("sharing")
	if sharing == "true" {
		res = gin.H{"link": h.config.SharingLink + objectName}
	}
	c.JSON(http.StatusOK, res)
	return
}

func (h *Handler) GetLinks(c *gin.Context) {
	key := c.Param("key")
	ios := c.Query("ios")
	stream := c.Query("stream")
	sharingLink := h.config.SharingLink + h.config.SharingDirectoryPrefix + key
	if ios != "" {
		c.Redirect(http.StatusMovedPermanently, "vlc://"+sharingLink)
		return
	}
	if stream != "" {
		c.Redirect(http.StatusMovedPermanently, sharingLink)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"key":    key,
		"stream": sharingLink,
		"iosVlc": "vlc://" + sharingLink,
	})
	return
}

// handleUploads uploads file to storage
func handleUploads(ctx context.Context, minioClient *minio.Client, bucketName, objectName, contentType string, file *multipart.FileHeader) (string, error) {
	src, err := file.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	n, err := minioClient.PutObject(ctx, bucketName, objectName, src, -1, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("Successfully uploaded %s of size %d\n", objectName, n)
	return objectName, nil
}

func handleDownload(ctx context.Context, minioClient *minio.Client, bucketName, objectName, filePath string) {
	err := minioClient.FGetObject(ctx, bucketName, objectName, filePath, minio.GetObjectOptions{})
	if err != nil {
		fmt.Println("ERRORRR: ", err)
	}
}

func handleMinio(ctx context.Context, config *Config) *minio.Client {
	minioClient, err := minio.New(config.Minio.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.Minio.AccessKeyID, config.Minio.SecretAccessKey, ""),
		Secure: config.Minio.UseSSL,
	})
	if err != nil {
		log.Fatalln(err)
	}

	err = minioClient.MakeBucket(ctx, config.Minio.BucketName, minio.MakeBucketOptions{Region: config.Minio.Location})
	if err != nil {
		exists, errBucketExists := minioClient.BucketExists(ctx, config.Minio.BucketName)
		if errBucketExists == nil && exists {
			log.Printf("We already own %s\n", config.Minio.BucketName)
		} else {
			log.Fatalln(err)
		}
	} else {
		log.Printf("Successfully created %s\n", config.Minio.BucketName)
	}
	return minioClient
}

func generateID() string {
	rand.Seed(time.Now().UTC().UnixNano())
	return strconv.FormatInt(rand.Int63n(999)+1000, 10)
}

type MinIOConfig struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
	BucketName      string
	Location        string
}

type Config struct {
	SharingLink            string
	Minio                  MinIOConfig
	SharingDirectoryPrefix string
	Port                   string
}

func NewConfig() (*Config, error) {
	var c *Config
	viper.SetConfigFile(viper.GetString("config"))
	err := viper.ReadInConfig()
	if err != nil {
		return &Config{}, fmt.Errorf("error opening config file: %s ", err)
	}

	err = viper.Unmarshal(&c)
	if err != nil {
		return &Config{}, fmt.Errorf("unable to decode config: %s ", err)
	}
	return c, nil
}

func init() {
	pflag.String("config", "", "config file path")
	pflag.Parse()

	err := viper.BindPFlags(pflag.CommandLine)
	if err != nil {
		panic(fmt.Errorf("Fatal error flags: %s ", err))
	}
}
