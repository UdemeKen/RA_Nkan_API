package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	config "github.com/olartbaraq/spectrumshelf/configs"
	db "github.com/olartbaraq/spectrumshelf/db/sqlc"
	"github.com/olartbaraq/spectrumshelf/utils"
	"github.com/redis/go-redis/v9"
	"gopkg.in/gomail.v2"
)

type User struct {
	server *Server
}

type UpdateUserParams struct {
	ID        int64     `json:"id" binding:"required"`
	Email     string    `json:"email" binding:"required,email"`
	Phone     string    `json:"phone" binding:"required,len=11"`
	Address   string    `json:"address" binding:"required"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UpdateUserPasswordParams struct {
	ID        int64     `json:"id" binding:"required"`
	Password  string    `json:"password" binding:"required,min=8" validate:"passwordStrength"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UserCodeInput struct {
	UserID int64  `json:"user_id" binding:"required"`
	Code   string `json:"code" binding:"required"`
}

type ForgotPasswordInput struct {
	Email string `form:"email"`
}

type UserResponse struct {
	ID        int64     `json:"id"`
	Lastname  string    `json:"lastname"`
	Firstname string    `json:"firstname"`
	Phone     string    `json:"phone"`
	Address   string    `json:"address"`
	Email     string    `json:"email"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DeleteUserParam struct {
	ID int64 `json:"id"`
}

func (u User) router(server *Server) {
	u.server = server
	serverGroup := server.router.Group("/users")
	serverGroup.GET("/allUsers", u.listUsers, AuthenticatedMiddleware())
	serverGroup.PUT("/update", u.updateUser, AuthenticatedMiddleware())
	serverGroup.PUT("/update/password", u.updatePassword, AuthenticatedMiddleware())
	serverGroup.DELETE("/deactivate", u.deleteUser, AuthenticatedMiddleware())
	serverGroup.GET("/profile", u.userProfile, AuthenticatedMiddleware())
	serverGroup.GET("/get_email", u.getUserEmail, AuthenticatedMiddleware())
	serverGroup.GET("/send_code_to_user", u.sendCodetoUser)
	serverGroup.POST("/verify_code", u.verifyCode)
}

//var VerificationCodes = make(map[int64]VerificationCode)

type VerificationResponse struct {
	UserID        int64
	GeneratedCode string
	ExpiresAt     time.Duration
	Email         string
}

var Rdb = redis.NewClient(&redis.Options{
	Addr:     "localhost:6379",
	Password: config.EnvRedisPassword(),
	DB:       0, // use default DB
})

func extractTokenFromRequest(ctx *gin.Context) (string, error) {
	// Extract the token from the Authorization header
	authorizationHeader := ctx.GetHeader("Authorization")
	if authorizationHeader == "" {
		return "", errors.New("unauthorized request")
	}

	// Expecting the header to be in the format "Bearer <token>"
	headerParts := strings.Split(authorizationHeader, " ")
	if len(headerParts) != 2 && strings.ToLower(headerParts[0]) != "bearer" {
		return "", errors.New("invalid token format")
	}

	return headerParts[1], nil
}

func returnIdRole(tokenString string) (int64, string, error) {

	if tokenString == "" {
		return 0, "", errors.New("unauthorized: Missing or invalid token")
	}

	userId, role, err := tokenManager.VerifyToken(&tokenString)

	if err != nil {
		return 0, "", errors.New("failed to verify token")
	}

	return userId, role, nil
}

func (u *User) listUsers(ctx *gin.Context) {

	tokenString, err := extractTokenFromRequest(ctx)

	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: Missing or invalid token",
		})
		return
	}

	_, role, err := returnIdRole(tokenString)

	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error":  err.Error(),
			"status": "failed to verify token",
		})
		ctx.Abort()
		return
	}

	if role != utils.AdminRole {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"message": "Unauthorized",
		})
		ctx.Abort()
		return
	}

	arg := db.ListAllUsersParams{
		Limit:  10,
		Offset: 0,
	}

	users, err := u.server.queries.ListAllUsers(context.Background(), arg)

	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error": err.Error(),
		})
		return
	}

	allUsers := []UserResponse{}

	for _, v := range users {

		userResponse := UserResponse{
			ID:        v.ID,
			Lastname:  v.Lastname,
			Firstname: v.Firstname,
			Email:     v.Email,
			Phone:     v.Phone,
			Address:   v.Address,
			IsAdmin:   v.IsAdmin,
			CreatedAt: v.CreatedAt,
		}
		n := userResponse
		allUsers = append(allUsers, n)
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "all users fetched sucessfully",
		"data":    allUsers,
	})
}

func (u *User) deleteUser(ctx *gin.Context) {

	tokenString, err := extractTokenFromRequest(ctx)

	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: Missing or invalid token",
		})
		ctx.Abort()
		return
	}

	userId, _, err := returnIdRole(tokenString)

	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error":  err.Error(),
			"status": "failed to verify token",
		})
		ctx.Abort()
		return
	}

	id := DeleteUserParam{}

	if userId != id.ID {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: invalid token",
		})
		ctx.Abort()
		return
	}

	if err := ctx.ShouldBindJSON(&id); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"Error": err.Error(),
		})
		return
	}

	err = u.server.queries.DeleteUser(context.Background(), id.ID)

	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error": err.Error(),
		})
		return
	}

	ctx.JSON(http.StatusAccepted, gin.H{
		"status":  "success",
		"message": "user deactivated sucessfully",
	})
}

func (u *User) updateUser(ctx *gin.Context) {

	tokenString, err := extractTokenFromRequest(ctx)

	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: Missing or invalid token",
		})
		return
	}

	userId, _, err := returnIdRole(tokenString)

	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error":  err.Error(),
			"status": "failed to verify token",
		})
		ctx.Abort()
		return
	}

	user := UpdateUserParams{}

	if err := ctx.ShouldBindJSON(&user); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"Error": err.Error(),
		})
		return
	}

	if userId != user.ID {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: invalid token",
		})
		ctx.Abort()
		return
	}

	arg := db.UpdateUserParams{
		ID:        user.ID,
		Email:     strings.ToLower(user.Email),
		Phone:     user.Phone,
		Address:   user.Address,
		UpdatedAt: time.Now(),
	}

	userToUpdate, err := u.server.queries.UpdateUser(context.Background(), arg)

	if err != nil {
		handleCreateUserError(ctx, err)
		return
	}

	userResponse := UserResponse{
		ID:        userToUpdate.ID,
		Lastname:  userToUpdate.Lastname,
		Firstname: userToUpdate.Firstname,
		Email:     userToUpdate.Email,
		Phone:     userToUpdate.Phone,
		Address:   userToUpdate.Address,
		IsAdmin:   userToUpdate.IsAdmin,
		CreatedAt: userToUpdate.CreatedAt,
		UpdatedAt: userToUpdate.UpdatedAt,
	}

	ctx.JSON(http.StatusAccepted, gin.H{
		"status":  "success",
		"message": "user updated successfully",
		"data":    userResponse,
	})
}

func (u *User) userProfile(ctx *gin.Context) {
	value, exist := ctx.Get("id")

	if !exist {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"status":  exist,
			"message": "Unauthorized",
		})
		return
	}

	userId, ok := value.(int64)

	if !ok {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"status":  exist,
			"message": "Issue Encountered, try again later",
		})
		return
	}

	user, err := u.server.queries.GetUserById(context.Background(), userId)

	if err == sql.ErrNoRows {
		ctx.JSON(http.StatusNotFound, gin.H{
			"Error":   err.Error(),
			"message": "Unauthorized",
		})
		return
	} else if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error":   err.Error(),
			"message": "Issue Encountered, try again later",
		})
		return
	}

	userResponse := UserResponse{
		ID:        user.ID,
		Lastname:  user.Lastname,
		Firstname: user.Firstname,
		Email:     user.Email,
		Phone:     user.Phone,
		Address:   user.Address,
		IsAdmin:   user.IsAdmin,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "user fetched successfully",
		"data":    userResponse,
	})
}

func (u *User) getUserEmail(ctx *gin.Context) {

	user := ForgotPasswordInput{}

	if err := ctx.ShouldBindQuery(&user); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"Error": err.Error(),
		})
		return
	}

	if strings.TrimSpace(user.Email) == "" {
		ctx.JSON(http.StatusNotFound, gin.H{
			"message": "no input entered",
		})
		return
	}

	userEmail, err := u.server.queries.GetUserByEmail(context.Background(), strings.ToLower(user.Email))

	if err == sql.ErrNoRows {
		ctx.JSON(http.StatusNotFound, gin.H{
			"statusCode": http.StatusNotFound,
			"Error":      err.Error(),
			"message":    "The requested user with the specified email does not exist.",
		})
		return
	} else if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error": err.Error(),
		})
		return
	}

	userResponse := UserResponse{
		ID:        userEmail.ID,
		Lastname:  userEmail.Lastname,
		Firstname: userEmail.Firstname,
		Email:     userEmail.Email,
		Phone:     userEmail.Phone,
		Address:   userEmail.Address,
		IsAdmin:   userEmail.IsAdmin,
		CreatedAt: userEmail.CreatedAt,
		UpdatedAt: userEmail.UpdatedAt,
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status":     "success",
		"statusCode": http.StatusOK,
		"message":    "user retrieved successfully",
		"data":       userResponse,
	})
}

func (u *User) sendCodetoUser(ctx *gin.Context) {
	// Bind User Input for validation

	user := ForgotPasswordInput{}

	if err := ctx.ShouldBindQuery(&user); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"Error": err.Error(),
		})
		return
	}

	if strings.TrimSpace(user.Email) == "" {
		ctx.JSON(http.StatusNotFound, gin.H{
			"message": "no input entered",
		})
		return
	}

	userGot, err := u.server.queries.GetUserByEmail(context.Background(), strings.ToLower(user.Email))

	if err == sql.ErrNoRows {
		ctx.JSON(http.StatusNotFound, gin.H{
			"statusCode": http.StatusNotFound,
			"Error":      err.Error(),
			"message":    "The requested user with the specified email does not exist.",
		})
		return
	} else if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error": err.Error(),
		})
		return
	}

	// GENERATE THE CODE AND STORE
	codeChan := make(chan string)

	go func(c chan string) {
		//Generate a 4 digit random code
		source := rand.NewSource(time.Now().UnixNano())
		rng := rand.New(source)
		code := rng.Intn(9000) + 1000
		c <- fmt.Sprintf("%04d", code)

	}(codeChan)

	returnedCode := <-codeChan

	stringUserId := fmt.Sprintf("%d", userGot.ID)

	timeout := 10 * time.Minute

	//fmt.Println("Did we get here?")

	err = Rdb.Set(ctx, stringUserId, returnedCode, timeout).Err()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"statusCode": http.StatusInternalServerError,
			"Error":      err.Error(),
		})
		return
	}

	// TODO: Send generated code to the user email address
	var wg sync.WaitGroup

	errorChan := make(chan error)

	wg.Add(1)

	//fmt.Println("About to enter send email goroutine")

	go func(userEmail, code string, e chan<- error) {
		defer wg.Done()

		//fmt.Println("About to read html")
		filereader, err := os.ReadFile("verification.html")
		if err != nil {
			e <- err
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"statusCode": http.StatusInternalServerError,
				"Error":      err.Error(),
			})
			ctx.Abort()
			return
		}

		messagetoSend := string(filereader)

		//fmt.Println("File converted")

		sender := config.EnvGoogleUsername()
		password := config.EnvGooglePassword()
		smtpHost := "smtp.gmail.com"
		smtpPort := 587

		message := gomail.NewMessage()
		message.SetHeader("From", sender)
		message.SetHeader("To", userEmail)
		message.SetHeader("Subject", "Verification Code")
		message.SetBody("text/plain", "Your verification code is: "+code)
		message.AddAlternative("text/html", messagetoSend+"Your verification code is: "+code)

		// Set up the email server configuration
		dialer := gomail.NewDialer(smtpHost, smtpPort, sender, password)

		//fmt.Println("we got to dialer")

		// Send the email
		if err := dialer.DialAndSend(message); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"statusCode": http.StatusInternalServerError,
				"Error":      err.Error(),
			})
			e <- err
			return
		}

		//fmt.Println("we sent the mail")

		e <- nil

	}(userGot.Email, returnedCode, errorChan)

	go func() {
		wg.Wait()
		close(errorChan)
	}()

	errVal := <-errorChan

	//fmt.Println("Email goroutine ended")

	coderesponse := VerificationResponse{
		UserID:        userGot.ID,
		GeneratedCode: returnedCode,
		ExpiresAt:     timeout,
		Email:         userGot.Email,
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status":     "success",
		"statusCode": http.StatusOK,
		"message":    "code sent to user successfully",
		"anyError":   errVal,
		"data":       coderesponse,
	})

	//VerificationCodes[userGot.ID] = VerificationCode{Code: returnedCode, ExpiresAt: returnedTime}
}

func (u *User) verifyCode(ctx *gin.Context) {

	codeInput := UserCodeInput{}

	if err := ctx.ShouldBindJSON(&codeInput); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"Error": err.Error(),
		})
		return
	}

	stringUserId := fmt.Sprintf("%d", codeInput.UserID)
	storedCode, err := Rdb.Get(ctx, stringUserId).Result()
	if err == redis.Nil {
		ctx.JSON(http.StatusNotFound, gin.H{
			"statusCode": http.StatusNotFound,
			"error":      err.Error(),
			"message":    "Key does not exist",
		})
		return
	} else if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"statusCode": http.StatusInternalServerError,
			"Error":      err.Error(),
		})
		return
	}

	timeout := 10 * time.Minute

	expirationTime := time.Now().Add(timeout)

	// Check if the code expiry is less than 10 min
	if time.Now().After(expirationTime) {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "code expired",
		})
		return
	}

	if codeInput.Code != storedCode {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"statusCode": http.StatusUnauthorized,
			"error":      "Invalid verification code",
		})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status":     "success",
		"statusCode": http.StatusOK,
		"message":    "code verification successful",
	})

	Rdb.Del(ctx, stringUserId)

	// Then call the update password endpoint.

}

func (u *User) updatePassword(ctx *gin.Context) {

	tokenString, err := extractTokenFromRequest(ctx)

	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: Missing or invalid token",
		})
		return
	}

	userId, _, err := returnIdRole(tokenString)

	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error":  err.Error(),
			"status": "failed to verify token",
		})
		ctx.Abort()
		return
	}

	user := UpdateUserPasswordParams{}

	if err := ctx.ShouldBindJSON(&user); err != nil {
		stringErr := string(err.Error())
		if strings.Contains(stringErr, "passwordStrength") {
			ctx.JSON(http.StatusBadRequest, gin.H{
				"Error": `
						"Password must be minimum of 8 characters",
						"Password must be contain at least a number",
						"Password must be contain at least a symbol",
						"Password must be contain a upper case letter"
						`,
			})
			ctx.Abort()
			return
		}

		ctx.JSON(http.StatusBadRequest, gin.H{
			"Error": err.Error(),
		})
		return
	}

	if userId != user.ID {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized: invalid token",
		})
		ctx.Abort()
		return
	}

	hashedPassword, err := utils.GenerateHashPassword(user.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error": err.Error(),
		})
		return
	}

	arg := db.UpdateUserPasswordParams{
		ID:             user.ID,
		HashedPassword: hashedPassword,
		UpdatedAt:      time.Now(),
	}

	userToUpdatePassword, err := u.server.queries.UpdateUserPassword(context.Background(), arg)

	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"Error": err.Error(),
		})
		return
	}

	userResponse := UserResponse{
		ID:        userToUpdatePassword.ID,
		Lastname:  userToUpdatePassword.Lastname,
		Firstname: userToUpdatePassword.Firstname,
		Email:     userToUpdatePassword.Email,
		Phone:     userToUpdatePassword.Phone,
		Address:   userToUpdatePassword.Address,
		IsAdmin:   userToUpdatePassword.IsAdmin,
		CreatedAt: userToUpdatePassword.CreatedAt,
		UpdatedAt: userToUpdatePassword.UpdatedAt,
	}

	ctx.JSON(http.StatusAccepted, gin.H{
		"status":  "success",
		"message": "password updated successfully",
		"data":    userResponse,
	})
}
