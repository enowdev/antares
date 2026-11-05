package server

import "github.com/enowdev/antares/internal/skills"

// currentSkills snapshots the live skill manager once per operation. The agent
// is authoritative when present; its stable manager pointer is reconfigured in
// place. Tests that construct a Server with no agent still use Options.Skills.
func (s *Server) currentSkills() *skills.Manager {
	if s.agent != nil {
		if m := s.agent.Skills(); m != nil {
			return m
		}
	}
	return s.skills
}
