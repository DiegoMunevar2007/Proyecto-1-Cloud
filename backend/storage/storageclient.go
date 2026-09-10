package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Buckets lógicos (misma instancia MinIO, prefijos/buckets separados).
const (
	BucketOriginals = "originals"
	BucketHLS       = "hls"
	BucketPublic    = "public"
)

// Client envuelve MinIO/S3 con URLs prefirmadas.
type Client struct {
	mc         *minio.Client
	presign    *minio.Client
	cdnBase    string
	useCDN     bool
	presignTTL time.Duration
}

// NewClient crea el cliente desde variables de entorno y asegura buckets.
func NewClient() (*Client, error) {
	endpoint := utils.GetEnv("S3_ENDPOINT", "localhost:9000")
	user := utils.GetEnv("S3_ACCESS_KEY", "minioadmin")
	pass := utils.GetEnv("S3_SECRET_KEY", "minioadmin")
	useSSL := utils.GetEnv("S3_USE_SSL", "false") == "true"
	region := utils.GetEnv("S3_REGION", "us-east-1")
	cdn := strings.TrimSuffix(utils.GetEnv("CDN_BASE_URL", ""), "/")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(user, pass, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	// Segundo cliente solo para firmar URLs con el endpoint público:
	// SigV4 incluye el host en la firma, por lo que la URL debe firmarse
	// con el mismo host que usará el navegador (S3_PUBLIC_ENDPOINT).
	presignEndpoint := endpoint
	presignSecure := useSSL
	if pub := strings.TrimSuffix(utils.GetEnv("S3_PUBLIC_ENDPOINT", ""), "/"); pub != "" {
		if pu, perr := url.Parse(pub); perr == nil && pu.Host != "" {
			presignEndpoint = pu.Host
			presignSecure = pu.Scheme == "https"
		}
	}
	pc, err := minio.New(presignEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(user, pass, ""),
		Secure: presignSecure,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	c := &Client{mc: mc, presign: pc, cdnBase: cdn, useCDN: cdn != "", presignTTL: 24 * time.Hour}
	ctx := context.Background()
	for _, b := range []string{BucketOriginals, BucketHLS, BucketPublic} {
		exists, err := mc.BucketExists(ctx, b)
		if err != nil {
			return nil, err
		}
		if !exists {
			if err := mc.MakeBucket(ctx, b, minio.MakeBucketOptions{Region: region}); err != nil {
				return nil, err
			}
		}
	}
	// Buckets public (thumbnails, HLS) con lectura anónima: el control de acceso
	// se verifica en la API antes de revelar la URL (rutas firmadas/UUID).
	publicRead := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`
	_ = mc.SetBucketPolicy(ctx, BucketPublic, fmt.Sprintf(publicRead, BucketPublic))
	_ = mc.SetBucketPolicy(ctx, BucketHLS, fmt.Sprintf(publicRead, BucketHLS))
	return c, nil
}

// PresignedPut genera una URL prefirmada de subida directa (multipart reanudable 24h).
func (c *Client) PresignedPut(bucket, objectKey string) (string, error) {
	if bucket == "" {
		bucket = BucketOriginals
	}
	u, err := c.presign.PresignedPutObject(context.Background(), bucket, objectKey, c.presignTTL)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PresignedGet genera una URL firmada de descarga tras verificar derecho de acceso.
// El controlador debe verificar rol/propiedad/inscripción antes de llamar.
func (c *Client) PresignedGet(bucket, objectKey string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > time.Hour {
		ttl = 15 * time.Minute
	}
	u, err := c.presign.PresignedGetObject(context.Background(), bucket, objectKey, ttl, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PublicURL retorna la URL de distribución (CDN si está configurado, si no endpoint S3 público).
func (c *Client) PublicURL(bucket, objectKey string) string {
	if c.useCDN {
		return fmt.Sprintf("%s/%s/%s", c.cdnBase, bucket, objectKey)
	}
	endpoint := utils.GetEnv("S3_PUBLIC_ENDPOINT", "http://localhost:9000")
	return fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(endpoint, "/"), bucket, objectKey)
}

// Stat retorna metadatos del objeto (para verificar integridad/MIME real).
func (c *Client) Stat(bucket, objectKey string) (minio.ObjectInfo, error) {
	return c.mc.StatObject(context.Background(), bucket, objectKey, minio.StatObjectOptions{})
}

// DownloadToFile descarga un objeto a un archivo local (uso del worker).
func (c *Client) DownloadToFile(bucket, objectKey, destPath string) error {
	obj, err := c.mc.GetObject(context.Background(), bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()
	return c.mc.FGetObject(context.Background(), bucket, objectKey, destPath, minio.GetObjectOptions{})
}

// UploadFile sube un archivo local (uso del worker para segmentos HLS).
func (c *Client) UploadFile(bucket, objectKey, srcPath, contentType string) error {
	_, err := c.mc.FPutObject(context.Background(), bucket, objectKey, srcPath, minio.PutObjectOptions{ContentType: contentType})
	return err
}
