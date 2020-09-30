package gui

import (
	"log"
	"strings"

	"github.com/fatih/color"
	"github.com/jesseduffield/gocui"
	"github.com/jesseduffield/lazygit/pkg/commands/models"
	"github.com/jesseduffield/lazygit/pkg/utils"
)

type CustomCommandObjects struct {
	SelectedLocalCommit  *models.Commit
	SelectedReflogCommit *models.Commit
	SelectedSubCommit    *models.Commit
	SelectedFile         *models.File
	SelectedLocalBranch  *models.Branch
	SelectedRemoteBranch *models.RemoteBranch
	SelectedRemote       *models.Remote
	SelectedTag          *models.Tag
	SelectedStashEntry   *models.StashEntry
	SelectedCommitFile   *models.CommitFile
	CheckedOutBranch     *models.Branch
	PromptResponses      []string
}

func (gui *Gui) resolveTemplate(templateStr string, promptResponses []string) (string, error) {
	objects := CustomCommandObjects{
		SelectedFile:         gui.getSelectedFile(),
		SelectedLocalCommit:  gui.getSelectedLocalCommit(),
		SelectedReflogCommit: gui.getSelectedReflogCommit(),
		SelectedLocalBranch:  gui.getSelectedBranch(),
		SelectedRemoteBranch: gui.getSelectedRemoteBranch(),
		SelectedRemote:       gui.getSelectedRemote(),
		SelectedTag:          gui.getSelectedTag(),
		SelectedStashEntry:   gui.getSelectedStashEntry(),
		SelectedCommitFile:   gui.getSelectedCommitFile(),
		SelectedSubCommit:    gui.getSelectedSubCommit(),
		CheckedOutBranch:     gui.currentBranch(),
		PromptResponses:      promptResponses,
	}

	return utils.ResolveTemplate(templateStr, objects)
}

func (gui *Gui) handleCustomCommandKeybinding(customCommand CustomCommand) func() error {
	return func() error {
		promptResponses := make([]string, len(customCommand.Prompts))

		f := func() error {
			cmdStr, err := gui.resolveTemplate(customCommand.Command, promptResponses)
			if err != nil {
				return gui.surfaceError(err)
			}

			if customCommand.Subprocess {
				gui.PrepareSubProcess(cmdStr)
				return nil
			}

			loadingText := customCommand.LoadingText
			if loadingText == "" {
				loadingText = gui.Tr.SLocalize("runningCustomCommandStatus")
			}
			return gui.WithWaitingStatus(loadingText, func() error {
				gui.OSCommand.PrepareSubProcess(cmdStr)

				if err := gui.OSCommand.RunCommand(cmdStr); err != nil {
					return gui.surfaceError(err)
				}
				return gui.refreshSidePanels(refreshOptions{})
			})
		}

		// if we have prompts we'll recursively wrap our confirm handlers with more prompts
		// until we reach the actual command
		for reverseIdx := range customCommand.Prompts {
			idx := len(customCommand.Prompts) - 1 - reverseIdx

			// going backwards so the outermost prompt is the first one
			prompt := customCommand.Prompts[idx]

			// need to do this because f's value will change with each iteration
			wrappedF := f

			switch prompt.Type {
			case "input":
				f = func() error {
					title, err := gui.resolveTemplate(prompt.Title, promptResponses)
					if err != nil {
						return gui.surfaceError(err)
					}

					initialValue, err := gui.resolveTemplate(prompt.InitialValue, promptResponses)
					if err != nil {
						return gui.surfaceError(err)
					}

					return gui.prompt(
						title,
						initialValue,
						func(str string) error {
							promptResponses[idx] = str

							return wrappedF()
						},
					)
				}
			case "menu":
				f = func() error {
					// need to make a menu here some how
					menuItems := make([]*menuItem, len(prompt.Options))
					for i, option := range prompt.Options {
						option := option

						nameTemplate := option.Name
						if nameTemplate == "" {
							// this allows you to only pass values rather than bother with names/descriptions
							nameTemplate = option.Value
						}
						name, err := gui.resolveTemplate(nameTemplate, promptResponses)
						if err != nil {
							return gui.surfaceError(err)
						}

						description, err := gui.resolveTemplate(option.Description, promptResponses)
						if err != nil {
							return gui.surfaceError(err)
						}

						value, err := gui.resolveTemplate(option.Value, promptResponses)
						if err != nil {
							return gui.surfaceError(err)
						}

						menuItems[i] = &menuItem{
							displayStrings: []string{name, utils.ColoredString(description, color.FgYellow)},
							onPress: func() error {
								promptResponses[idx] = value

								return wrappedF()
							},
						}
					}

					title, err := gui.resolveTemplate(prompt.Title, promptResponses)
					if err != nil {
						return gui.surfaceError(err)
					}

					return gui.createMenu(title, menuItems, createMenuOptions{showCancel: true})
				}
			default:
				return gui.createErrorPanel("custom command prompt must have a type of 'input' or 'menu'")
			}

		}

		return f()
	}
}

type CustomCommandMenuOption struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Value       string `yaml:"value"`
}

type CustomCommandPrompt struct {
	Type  string `yaml:"type"` // one of 'input' and 'menu'
	Title string `yaml:"title"`

	// this only apply to prompts
	InitialValue string `yaml:"initialValue"`

	// this only applies to menus
	Options []CustomCommandMenuOption
}

type CustomCommand struct {
	Key         string                `yaml:"key"`
	Context     string                `yaml:"context"`
	Command     string                `yaml:"command"`
	Subprocess  bool                  `yaml:"subprocess"`
	Prompts     []CustomCommandPrompt `yaml:"prompts"`
	LoadingText string                `yaml:"loadingText"`
	Description string                `yaml:"description"`
}

func (gui *Gui) GetCustomCommandKeybindings() []*Binding {
	bindings := []*Binding{}

	var customCommands []CustomCommand

	if err := gui.Config.GetUserConfig().UnmarshalKey("customCommands", &customCommands); err != nil {
		log.Fatalf("Error parsing custom command keybindings: %v", err)
	}

	for _, customCommand := range customCommands {
		var viewName string
		var contexts []string
		switch customCommand.Context {
		case "global":
			viewName = ""
		case "":
			log.Fatalf("Error parsing custom command keybindings: context not provided (use context: 'global' for the global context). Key: %s, Command: %s", customCommand.Key, customCommand.Command)
		default:
			context, ok := gui.contextForContextKey(customCommand.Context)
			if !ok {
				log.Fatalf("Error when setting custom command keybindings: unknown context: %s. Key: %s, Command: %s.\nPermitted contexts: %s", customCommand.Context, customCommand.Key, customCommand.Command, strings.Join(allContextKeys, ", "))
			}
			// here we assume that a given context will always belong to the same view.
			// Currently this is a safe bet but it's by no means guaranteed in the long term
			// and we might need to make some changes in the future to support it.
			viewName = context.GetViewName()
			contexts = []string{customCommand.Context}
		}

		description := customCommand.Description
		if description == "" {
			description = customCommand.Command
		}

		bindings = append(bindings, &Binding{
			ViewName:    viewName,
			Contexts:    contexts,
			Key:         gui.getKey(customCommand.Key),
			Modifier:    gocui.ModNone,
			Handler:     gui.wrappedHandler(gui.handleCustomCommandKeybinding(customCommand)),
			Description: description,
		})
	}

	return bindings
}
