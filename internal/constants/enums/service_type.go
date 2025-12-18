package enums

import "fmt"

// ServiceType 表示具体的业务服务类型，直接使用 string 作为底层类型，方便序列化与配置。
type ServiceType string

const (
	ServiceTypeGeneric ServiceType = "generic"
	ServiceTypeOP      ServiceType = "op"
	ServiceTypeCDK     ServiceType = "cdk"
	ServiceTypeXJST    ServiceType = "xjst"
)

func (t ServiceType) String() string {
	return string(t)
}

// ParseServiceType 将字符串解析为 ServiceType，默认解析失败时返回错误。
func ParseServiceType(s string) (ServiceType, error) {
	switch ServiceType(s) {
	case "", ServiceTypeGeneric:
		return ServiceTypeGeneric, nil
	case ServiceTypeOP:
		return ServiceTypeOP, nil
	case ServiceTypeCDK:
		return ServiceTypeCDK, nil
	case ServiceTypeXJST:
		return ServiceTypeXJST, nil
	default:
		return "", fmt.Errorf("不支持的 service 类型: %s", s)
	}
}



