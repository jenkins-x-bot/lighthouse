/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hook

import (
	"strconv"
	"sync"

	"github.com/jenkins-x/go-scm/scm"
	"github.com/jenkins-x/jx/pkg/jxfactory"
	"github.com/jenkins-x/lighthouse/pkg/prow/config"
	"github.com/sirupsen/logrus"

	"github.com/jenkins-x/lighthouse/pkg/prow/gitprovider"
	"github.com/jenkins-x/lighthouse/pkg/prow/plugins"
)

// Server keeps the information required to start a server
type Server struct {
	ClientFactory  jxfactory.Factory
	ClientAgent    *plugins.ClientAgent
	Plugins        *plugins.ConfigAgent
	ConfigAgent    *config.Agent
	TokenGenerator func() []byte
	Metrics        *Metrics

	// Tracks running handlers for graceful shutdown
	wg sync.WaitGroup
}

const failedCommentCoerceFmt = "Could not coerce %s event to a GenericCommentEvent. Unknown 'action': %q."

// HandleIssueCommentEvent handle comment events
func (s *Server) HandleIssueCommentEvent(l *logrus.Entry, ic scm.IssueCommentHook) {
	l = l.WithFields(logrus.Fields{
		gitprovider.OrgLogField:  ic.Repo.Namespace,
		gitprovider.RepoLogField: ic.Repo.Name,
		gitprovider.PrLogField:   ic.Issue.Number,
		"author":                 ic.Comment.Author.Login,
		"url":                    ic.Comment.Link,
	})
	l.Infof("Issue comment %s.", ic.Action)
	for p, h := range s.Plugins.IssueCommentHandlers(ic.Repo.Namespace, ic.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.IssueCommentHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ClientFactory, s.ConfigAgent, s.Plugins, s.ClientAgent, l.WithField("plugin", p))
			agent.InitializeCommentPruner(
				ic.Repo.Namespace,
				ic.Repo.Name,
				ic.Issue.Number,
			)
			if err := h(agent, ic); err != nil {
				agent.Logger.WithError(err).Error("Error handling IssueCommentEvent.")
			}
		}(p, h)
	}

	s.handleGenericComment(
		l,
		&gitprovider.GenericCommentEvent{
			GUID:        strconv.Itoa(ic.Comment.ID),
			IsPR:        ic.Issue.PullRequest,
			Action:      ic.Action,
			Body:        ic.Comment.Body,
			Link:        ic.Comment.Link,
			Number:      ic.Issue.Number,
			Repo:        ic.Repo,
			Author:      ic.Comment.Author,
			IssueAuthor: ic.Issue.Author,
			Assignees:   ic.Issue.Assignees,
			IssueState:  ic.Issue.State,
			IssueBody:   ic.Issue.Body,
			IssueLink:   ic.Issue.Link,
		},
	)
}

// HandlePullRequestCommentEvent handles pull request comments events
func (s *Server) HandlePullRequestCommentEvent(l *logrus.Entry, pc scm.PullRequestCommentHook) {
	l = l.WithFields(logrus.Fields{
		gitprovider.OrgLogField:  pc.Repo.Namespace,
		gitprovider.RepoLogField: pc.Repo.Name,
		gitprovider.PrLogField:   pc.PullRequest.Number,
		"author":                 pc.Comment.Author.Login,
		"url":                    pc.Comment.Link,
	})
	l.Infof("PR comment %s.", pc.Action)

	s.handleGenericComment(
		l,
		&gitprovider.GenericCommentEvent{
			GUID:        strconv.Itoa(pc.Comment.ID),
			IsPR:        true,
			Action:      pc.Action,
			Body:        pc.Comment.Body,
			Link:        pc.Comment.Link,
			Number:      pc.PullRequest.Number,
			Repo:        pc.Repo,
			Author:      pc.Comment.Author,
			IssueAuthor: pc.PullRequest.Author,
			Assignees:   pc.PullRequest.Assignees,
			IssueState:  pc.PullRequest.State,
			IssueBody:   pc.PullRequest.Body,
			IssueLink:   pc.PullRequest.Link,
		},
	)
}

func (s *Server) handleGenericComment(l *logrus.Entry, ce *gitprovider.GenericCommentEvent) {
	for p, h := range s.Plugins.GenericCommentHandlers(ce.Repo.Namespace, ce.Repo.Name) {
		s.wg.Add(1)
		go func(p string, h plugins.GenericCommentHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ClientFactory, s.ConfigAgent, s.Plugins, s.ClientAgent, l.WithField("plugin", p))
			agent.InitializeCommentPruner(
				ce.Repo.Namespace,
				ce.Repo.Name,
				ce.Number,
			)
			if err := h(agent, *ce); err != nil {
				agent.Logger.WithError(err).Error("Error handling GenericCommentEvent.")
			}
		}(p, h)
	}
}

// HandlePushEvent handles a push event
func (s *Server) HandlePushEvent(l *logrus.Entry, pe *scm.PushHook) {
	repo := pe.Repository()
	l = l.WithFields(logrus.Fields{
		gitprovider.OrgLogField:  repo.Namespace,
		gitprovider.RepoLogField: repo.Name,
		"ref":                    pe.Ref,
		"head":                   pe.After,
	})
	l.Info("Push event.")
	c := 0
	for p, h := range s.Plugins.PushEventHandlers(repo.Namespace, repo.Name) {
		s.wg.Add(1)
		c++
		go func(p string, h plugins.PushEventHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ClientFactory, s.ConfigAgent, s.Plugins, s.ClientAgent, l.WithField("plugin", p))
			if err := h(agent, *pe); err != nil {
				agent.Logger.WithError(err).Error("Error handling PushEvent.")
			}
		}(p, h)
	}
	l.WithField("count", strconv.Itoa(c)).Info("number of push handlers")
}

// HandlePullRequestEvent handles a pull request event
func (s *Server) HandlePullRequestEvent(l *logrus.Entry, pr *scm.PullRequestHook) {
	l = l.WithFields(logrus.Fields{
		gitprovider.OrgLogField:  pr.Repo.Namespace,
		gitprovider.RepoLogField: pr.Repo.Name,
		gitprovider.PrLogField:   pr.PullRequest.Number,
		"author":                 pr.PullRequest.Author.Login,
		"url":                    pr.PullRequest.Link,
	})
	action := pr.Action
	l.Infof("Pull request %s.", action)
	c := 0
	repo := pr.PullRequest.Base.Repo
	if repo.Name == "" {
		repo = pr.Repo
	}
	for p, h := range s.Plugins.PullRequestHandlers(repo.Namespace, repo.Name) {
		s.wg.Add(1)
		c++
		go func(p string, h plugins.PullRequestHandler) {
			defer s.wg.Done()
			agent := plugins.NewAgent(s.ClientFactory, s.ConfigAgent, s.Plugins, s.ClientAgent, l.WithField("plugin", p))
			agent.InitializeCommentPruner(
				pr.Repo.Namespace,
				pr.Repo.Name,
				pr.PullRequest.Number,
			)
			if err := h(agent, *pr); err != nil {
				agent.Logger.WithError(err).Error("Error handling PullRequestEvent.")
			}
		}(p, h)
	}
	l.WithField("count", strconv.Itoa(c)).Info("number of PR handlers")

	if !actionRelatesToPullRequestComment(action, l) {
		return
	}
	s.handleGenericComment(
		l,
		&gitprovider.GenericCommentEvent{
			GUID:        pr.GUID,
			IsPR:        true,
			Action:      action,
			Body:        pr.PullRequest.Body,
			Link:        pr.PullRequest.Link,
			Number:      pr.PullRequest.Number,
			Repo:        pr.Repo,
			Author:      pr.PullRequest.Author,
			IssueAuthor: pr.PullRequest.Author,
			Assignees:   pr.PullRequest.Assignees,
			IssueState:  pr.PullRequest.State,
			IssueBody:   pr.PullRequest.Body,
			IssueLink:   pr.PullRequest.Link,
		},
	)
}

// HandleBranchEvent handles a branch event
func (s *Server) HandleBranchEvent(entry *logrus.Entry, hook *scm.BranchHook) {
	// TODO
}

func actionRelatesToPullRequestComment(action scm.Action, l *logrus.Entry) bool {
	switch action {

	case scm.ActionCreate, scm.ActionOpen, scm.ActionSubmitted, scm.ActionEdited, scm.ActionDelete, scm.ActionDismissed:
		return true

	case scm.ActionAssigned,
		scm.ActionUnassigned,
		scm.ActionReviewRequested,
		scm.ActionReviewRequestRemoved,
		scm.ActionLabel,
		scm.ActionUnlabel,
		scm.ActionClose,
		scm.ActionReopen,
		scm.ActionSync:
		return false

	default:
		l.Errorf(failedCommentCoerceFmt, "pull_request", string(action))
		return false
	}
}
