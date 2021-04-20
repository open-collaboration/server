package projects

import (
	"errors"
	"github.com/gorilla/mux"
	"github.com/lib/pq"
	"github.com/open-collaboration/server/auth"
	"github.com/open-collaboration/server/utils"
	"net/http"
	"strconv"
	"strings"
)

// @Summary Create a project
// @Tags projects
// @Router /projects [post]
// @Param project body dtos.NewProjectDto true "Project data"
// @Success 200 {object} dtos.ProjectSummaryDto.
func RouteCreateProject(
	writer http.ResponseWriter,
	request *http.Request,
	projectsService *Service,
) error {
	_, err := auth.CheckSession(request)
	if err != nil {
		return err
	}

	dto := NewProjectDto{}
	err = utils.ReadJson(request.Context(), request, &dto)
	if err != nil {
		return err
	}

	createdProject, err := projectsService.CreateProject(dto)
	if err != nil {
		return err
	}

	projectSummary := projectsService.GetProjectSummary(createdProject)

	writer.Header().Set("Location", "/projects/"+strconv.Itoa(int(createdProject.ID)))

	err = utils.WriteJson(writer, request.Context(), http.StatusCreated, projectSummary)
	if err != nil {
		return err
	}

	return nil
}

// @Summary List all projects
// @Tags projects
// @Router /projects [get]
// @Param pageSize query int false "Maximum amount of projects in the response. Default is 20, max is 20."
// @Param pageOffset query int false "Response page number. If pageSize is 20 and pageOffset is 2, the first 40 projects will be skipped."
// @Success 200 {object} dtos.ProjectSummaryDto.
func RouteListProjects(writer http.ResponseWriter, request *http.Request, projectsService *Service) error {
	// TODO: move hardcoded maximum and default page size values to
	// 	an env variable
	pageSize, _ := utils.IntFromQuery(request, "pageSize", 20)
	pageOffset, _ := utils.IntFromQuery(request, "pageOffset", 0)

	var tags []string
	if len(request.URL.Query()["tags"]) > 0 {
		println(len(request.URL.Query()["tags"]))
		tagsRaw := strings.Join(request.URL.Query()["tags"], ",")
		tags = strings.Split(tagsRaw, ",")
	}

	if pageSize < 1 || pageSize > 20 {
		pageSize = 20
	}

	if pageOffset < 1 {
		pageOffset = 0
	}

	projectSummaries, err := projectsService.ListProjects(request.Context(), uint(pageSize), uint(pageOffset), tags, []string{})
	if err != nil {
		return err
	}

	for i := range projectSummaries {
		projectSummaries[i].Skills = pq.StringArray{}
	}

	err = utils.WriteJson(writer, request.Context(), http.StatusOK, projectSummaries)
	if err != nil {
		return err
	}

	return nil
}

// @Summary Get project
// @Tags projects
// @Router /projects/{id} [get]
// @Param id path int true "The project ID"
// @Success 200 {object} dtos.ProjectDto.
func RouteGetProject(writer http.ResponseWriter, request *http.Request, projectsService *Service) error {
	var projectId uint
	vars := mux.Vars(request)
	if idStr, ok := vars["projectId"]; ok {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			return err
		}

		projectId = uint(id)
	}

	dto, err := projectsService.GetProject(request.Context(), projectId)
	if err != nil {
		if errors.Is(err, ProjectNotFoundError) {
			writer.WriteHeader(404)
			return nil
		} else {
			return err
		}
	}

	err = utils.WriteJson(writer, request.Context(), http.StatusOK, dto)
	if err != nil {
		return err
	}

	return nil
}