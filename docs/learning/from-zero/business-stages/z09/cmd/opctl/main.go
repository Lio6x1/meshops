package main
import("context";"os";"os/signal";"syscall";"example.com/meshops-course/internal/cli")
func main(){ctx,cancel:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM);code:=cli.Opctl(ctx,os.Args[1:],os.Stdout,os.Stderr);cancel();os.Exit(code)}
