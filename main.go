package main

import (
	"encoding/binary"
	"fmt"
	"github.com/beevik/ntp"
	rotatelogs "github.com/lestrrat/go-file-rotatelogs"
	"github.com/pkg/errors"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ServerIp             string
	TotalThread          int
	TotalRquestPerThread int
	CaseId               int
	Log                  *logrus.Logger
)

func init() {
	path, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	viper.AddConfigPath(path)
	viper.SetConfigName("conf")
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		panic(err)
	}

	ServerIp = viper.GetString("ntp.serverIp")
	TotalThread = viper.GetInt("perf.totalThread")
	TotalRquestPerThread = viper.GetInt("perf.totalRquestPerThread")
	CaseId = viper.GetInt("perf.caseId")

	logFile := viper.GetString("log.logFile")
	maxAgeHours := viper.GetDuration("log.maxAgeHours")
	rotationHours := viper.GetDuration("log.rotationHours")
	logLevel := viper.GetString("log.level")
	Log = NewRotateLogger(logFile, logLevel, maxAgeHours, rotationHours)
	Log.Info("== start ==")
}

func NewRotateLogger(fn, logLevel string, maxAgeHours, rotationHours time.Duration) *logrus.Logger {
	// 日志路径必须是绝对路径，不能使用相对路径
	logger := NewLogger(fn, maxAgeHours*time.Hour, rotationHours*time.Hour)
	switch strings.ToLower(logLevel) {
	case "debug":
		logger.Level = logrus.DebugLevel
	case "info":
		logger.Level = logrus.InfoLevel
	case "warn":
		logger.Level = logrus.WarnLevel
	case "error":
		logger.Level = logrus.ErrorLevel
	}
	logger.SetReportCaller(true)
	return logger
}

func NewLogger(logFile string, maxAge time.Duration, rotationTime time.Duration) *logrus.Logger {
	writer, err := rotatelogs.New(
		logFile+".%Y%m%d",
		rotatelogs.WithLinkName(logFile),          //生成软链，指向最新日志文件
		rotatelogs.WithMaxAge(maxAge),             //文件最大保存时间
		rotatelogs.WithRotationTime(rotationTime), //日志切割时间间隔
	)
	if err != nil {
		fmt.Println("Create log file error.%+v", errors.WithStack(err))
		os.Exit(1)
	}

	lfHook := lfshook.NewHook(lfshook.WriterMap{
		logrus.DebugLevel: writer, // 为不同级别设置不同的输出目的
		logrus.InfoLevel:  writer,
		logrus.WarnLevel:  writer,
		logrus.ErrorLevel: writer,
		logrus.FatalLevel: writer,
		logrus.PanicLevel: writer,
	},
		&logrus.TextFormatter{},
	)

	logger := logrus.New()
	logger.AddHook(lfHook)
	return logger
}

func TestCase1() error {
	_, err := ntp.Time(ServerIp)
	if err != nil {
		Log.Debug(err.Error())
	}
	return err
}

func TestCase2() error {
	options := ntp.QueryOptions{Timeout: 30 * time.Second, TTL: 5}
	_, err := ntp.QueryWithOptions(ServerIp, options)
	if err != nil {
		Log.Debug(err.Error())
	}
	return err
}

func TestCase3() (err error) {
	var conn net.Conn
	address := net.JoinHostPort(ServerIp, "123")
	conn, err = net.Dial("udp", address)
	if err != nil {
		Log.Error(fmt.Sprintf("fail to dial %s, err: %v", address, err))
		return
		//continue
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		Log.Error("fail to set deadline for connection")
		return
	}

	// RFC: https://datatracker.ietf.org/doc/html/rfc4330#section-4
	// NTP Packet is 48 bytes and we set the first byte for request.
	// 00 100 011 (or 0x2B)
	// |  |   +-- client mode (3)
	// |  + ----- version (4)
	// + -------- leap year indicator, 0 no warning
	req := make([]byte, 48)
	req[0] = 0x2B

	// send time request
	if err = binary.Write(conn, binary.BigEndian, req); err != nil {
		Log.Error("fail to send NTP request")
		//continue
		return
	}

	// block to receive server response
	rsp := make([]byte, 48)
	if err = binary.Read(conn, binary.BigEndian, &rsp); err != nil {
		Log.Error("fail to receive NTP response")
		return
		//continue
	}
	return nil
}

func main() {
	type Fun func() error
	var TestFun Fun
	switch CaseId {
	case 1:
		TestFun = TestCase1
	case 2:
		TestFun = TestCase2
	case 3:
		TestFun = TestCase3
	}

	var TotalError int = 0
	var MaxSecond int64 = 0

	var waitGroup sync.WaitGroup
	waitGroup.Add(TotalThread)

	for t := 0; t < TotalThread; t++ {
		go func(ti int) {
			defer waitGroup.Done()
			count := 0
			ts1 := time.Now().Unix()
			for i := 1; i <= TotalRquestPerThread; i++ {
				//fmt.Println("====")
				if TestFun() != nil {
					count = count + 1
				}
			}
			ts2 := time.Now().Unix()
			TotalError = TotalError + count
			deltaSecond := ts2 - ts1
			if deltaSecond > MaxSecond {
				MaxSecond = deltaSecond
			}
			Log.Info(fmt.Sprintf("thread-%02d 共请求: %d 次，耗时: %ds, 错误数: %d 个 ", ti, TotalRquestPerThread, ts2-ts1, count))
		}(t)
	}
	waitGroup.Wait()
	Log.Info(fmt.Sprintf("共请求: %d 次，耗时: %ds, 错误数: %d 个, QPS: %d ", TotalThread*TotalRquestPerThread, MaxSecond, TotalError, int64(TotalThread*TotalRquestPerThread)/MaxSecond))
	Log.Info("== End ==")
}
