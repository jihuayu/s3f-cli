package cli

import (
	"context"
	"fmt"
	"io"
	"sort"

	"s3f-cli/internal/editor"
	"s3f-cli/internal/model"
	"s3f-cli/internal/vfs"
)

type Command interface {
	Name() string
	Run(ctx context.Context, sess *model.Session, args []string) error
}

type Dependencies struct {
	Resolver vfs.PathResolver
	VFS      vfs.VFS
	Editor   editor.Editor
	Stdout   io.Writer
}

type Registry struct {
	commands map[string]Command
}

func NewRegistry(commands ...Command) *Registry {
	items := make(map[string]Command, len(commands))
	for _, command := range commands {
		items[command.Name()] = command
	}
	return &Registry{commands: items}
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Lookup(name string) (Command, bool) {
	command, ok := r.commands[name]
	return command, ok
}

type PwdCommand struct {
	Out io.Writer
}

func (c PwdCommand) Name() string { return "pwd" }

func (c PwdCommand) Run(_ context.Context, sess *model.Session, _ []string) error {
	if sess == nil {
		return model.InvalidPath("pwd", "", "session is not initialized")
	}
	sess.EnsureDefaults()
	_, err := fmt.Fprintln(c.Out, sess.Cwd)
	return err
}

type CdCommand struct {
	Resolver vfs.PathResolver
	VFS      vfs.VFS
}

func (c CdCommand) Name() string { return "cd" }

func (c CdCommand) Run(ctx context.Context, sess *model.Session, args []string) error {
	if sess == nil {
		return model.InvalidPath("cd", "", "session is not initialized")
	}
	if c.Resolver == nil || c.VFS == nil {
		return model.Unsupported("cd", "", "resolver and vfs are required")
	}

	target := ""
	if len(args) > 0 {
		target = args[0]
	}

	resolved, err := c.Resolver.Resolve(sess, target)
	if err != nil {
		return err
	}

	node, err := c.VFS.Stat(ctx, resolved, vfs.StatOptions{AllowMarker: true})
	if err != nil {
		return err
	}
	if node.Kind != model.NodeKindDir {
		return model.InvalidPath("cd", resolved.RemotePath(), "target is not a directory")
	}

	sess.Apply(resolved)
	return nil
}

type LsCommand struct {
	Resolver vfs.PathResolver
	VFS      vfs.VFS
	Out      io.Writer
	Long     bool
}

func (c LsCommand) Name() string {
	if c.Long {
		return "ll"
	}
	return "ls"
}

func (c LsCommand) Run(ctx context.Context, sess *model.Session, args []string) error {
	if c.Resolver == nil || c.VFS == nil {
		return model.Unsupported(c.Name(), "", "resolver and vfs are required")
	}

	target := ""
	if len(args) > 0 {
		target = args[0]
	}

	resolved, err := c.Resolver.Resolve(sess, target)
	if err != nil {
		return err
	}

	nodes, err := c.VFS.List(ctx, resolved, vfs.ListOptions{LongFormat: c.Long})
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if c.Long {
			if _, err := fmt.Fprintf(c.Out, "%-10s %12d %s %s\n", node.SyntheticMode, node.Size, node.ModTime.Format("2006-01-02 15:04:05"), node.Name); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(c.Out, node.Name); err != nil {
			return err
		}
	}
	return nil
}

type CatCommand struct {
	Resolver vfs.PathResolver
	VFS      vfs.VFS
	Out      io.Writer
}

func (c CatCommand) Name() string { return "cat" }

func (c CatCommand) Run(ctx context.Context, sess *model.Session, args []string) error {
	if len(args) == 0 {
		return model.InvalidPath("cat", "", "missing object path")
	}
	if c.Resolver == nil || c.VFS == nil {
		return model.Unsupported("cat", "", "resolver and vfs are required")
	}

	resolved, err := c.Resolver.Resolve(sess, args[0])
	if err != nil {
		return err
	}

	reader, err := c.VFS.Read(ctx, resolved, vfs.ReadOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()

	_, err = io.Copy(c.Out, reader)
	return err
}

type MkdirCommand struct {
	Resolver vfs.PathResolver
	VFS      vfs.VFS
}

func (c MkdirCommand) Name() string { return "mkdir" }

func (c MkdirCommand) Run(ctx context.Context, sess *model.Session, args []string) error {
	if len(args) == 0 {
		return model.InvalidPath("mkdir", "", "missing directory path")
	}
	if c.Resolver == nil || c.VFS == nil {
		return model.Unsupported("mkdir", "", "resolver and vfs are required")
	}

	resolved, err := c.Resolver.Resolve(sess, args[0]+"/")
	if err != nil {
		return err
	}

	return c.VFS.MakeDirAll(ctx, resolved)
}

type CpCommand struct {
	Resolver vfs.PathResolver
	VFS      vfs.VFS
	Options  vfs.CopyOptions
}

func (c CpCommand) Name() string { return "cp" }

func (c CpCommand) Run(ctx context.Context, sess *model.Session, args []string) error {
	if len(args) < 2 {
		return model.InvalidPath("cp", "", "cp requires source and destination")
	}
	if c.Resolver == nil || c.VFS == nil {
		return model.Unsupported("cp", "", "resolver and vfs are required")
	}

	src, err := c.Resolver.Resolve(sess, args[0])
	if err != nil {
		return err
	}
	dst, err := c.Resolver.Resolve(sess, args[1])
	if err != nil {
		return err
	}
	return c.VFS.Copy(ctx, src, dst, c.Options)
}

type MvCommand struct {
	Resolver vfs.PathResolver
	VFS      vfs.VFS
	Options  vfs.MoveOptions
}

func (c MvCommand) Name() string { return "mv" }

func (c MvCommand) Run(ctx context.Context, sess *model.Session, args []string) error {
	if len(args) < 2 {
		return model.InvalidPath("mv", "", "mv requires source and destination")
	}
	if c.Resolver == nil || c.VFS == nil {
		return model.Unsupported("mv", "", "resolver and vfs are required")
	}

	src, err := c.Resolver.Resolve(sess, args[0])
	if err != nil {
		return err
	}
	dst, err := c.Resolver.Resolve(sess, args[1])
	if err != nil {
		return err
	}

	result, err := c.VFS.Move(ctx, src, dst, c.Options)
	if err != nil {
		return err
	}
	if result.Partial {
		return model.NewError(model.ErrNonAtomicMove, "mv", src.RemotePath(), "move completed partially; source cleanup may be incomplete", nil)
	}
	return nil
}

type FindCommand struct {
	Resolver vfs.PathResolver
	VFS      vfs.VFS
	Out      io.Writer
	Options  vfs.FindOptions
}

func (c FindCommand) Name() string { return "find" }

func (c FindCommand) Run(ctx context.Context, sess *model.Session, args []string) error {
	if c.Resolver == nil || c.VFS == nil {
		return model.Unsupported("find", "", "resolver and vfs are required")
	}

	target := ""
	if len(args) > 0 {
		target = args[0]
	}

	resolved, err := c.Resolver.Resolve(sess, target)
	if err != nil {
		return err
	}

	nodes, err := c.VFS.Find(ctx, resolved, c.Options)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if _, err := fmt.Fprintln(c.Out, node.Path); err != nil {
			return err
		}
	}
	return nil
}

type ViCommand struct {
	Resolver vfs.PathResolver
	Editor   editor.Editor
}

func (c ViCommand) Name() string { return "vi" }

func (c ViCommand) Run(ctx context.Context, sess *model.Session, args []string) error {
	if len(args) == 0 {
		return model.InvalidPath("vi", "", "missing object path")
	}
	if c.Resolver == nil || c.Editor == nil {
		return model.Unsupported("vi", "", "resolver and editor are required")
	}

	resolved, err := c.Resolver.Resolve(sess, args[0])
	if err != nil {
		return err
	}

	return c.Editor.Edit(ctx, editor.Session{Remote: resolved}, false)
}
