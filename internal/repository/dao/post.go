package dao

import (
	"context"
	"errors"
	"github.com/GoSimplicity/LinkMe/internal/domain"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"time"
)

// 用于在上下文中存储钩子执行状态的键
type contextKey string

const hookExecutedKey contextKey = "hookExecuted"

var (
	ErrPostNotFound  = errors.New("post not found")
	ErrInvalidParams = errors.New("invalid parameters")
)

type PostDAO interface {
	Insert(ctx context.Context, post Post) (uint, error)                              // 创建一个新的帖子记录
	UpdateById(ctx context.Context, post Post) error                                  // 根据ID更新一个帖子记录
	UpdateStatus(ctx context.Context, post Post) error                                // 更新帖子的状态
	GetById(ctx context.Context, postId uint, uid int64) (Post, error)                // 根据ID获取一个帖子记录
	GetPubById(ctx context.Context, postId uint) (ListPubPost, error)                 // 根据ID获取一个已发布的帖子记录
	ListPub(ctx context.Context, pagination domain.Pagination) ([]ListPubPost, error) // 获取已发布的帖子记录列表
	List(ctx context.Context, pagination domain.Pagination) ([]Post, error)           // 获取个人的帖子记录列表
	DeleteById(ctx context.Context, post Post) error
	ListAllPost(ctx context.Context, pagination domain.Pagination) ([]Post, error)
	GetPost(ctx context.Context, postId uint) (Post, error)
	GetPostCount(ctx context.Context) (int64, error)
}

type postDAO struct {
	client *mongo.Client
	l      *zap.Logger
	db     *gorm.DB
}

type Post struct {
	gorm.Model
	Title        string `gorm:"size:255;not null"`            // 文章标题
	Content      string `gorm:"type:text;not null"`           // 文章内容
	Status       uint8  `gorm:"default:0"`                    // 文章状态，如草稿、发布等
	AuthorID     int64  `gorm:"column:author_id;index"`       // 用户uid
	Slug         string `gorm:"size:100;uniqueIndex"`         // 文章的唯一标识，用于生成友好URL
	CategoryID   int64  `gorm:"index"`                        // 关联分类表的外键
	PlateID      int64  `gorm:"index"`                        // 关联板块表的外键
	Plate        Plate  `gorm:"foreignKey:PlateID"`           // 板块关系
	Tags         string `gorm:"type:varchar(255);default:''"` // 文章标签，以逗号分隔
	CommentCount int64  `gorm:"default:0"`                    // 文章的评论数量
}

type ListPubPost struct {
	ID           uint      `bson:"id"`           // MongoDB的ObjectID
	CreatedAt    time.Time `bson:"createdat"`    // 创建时间
	UpdatedAt    time.Time `bson:"updatedat"`    // 更新时间
	Title        string    `bson:"title"`        // 文章标题
	Content      string    `bson:"content"`      // 文章内容
	Status       uint8     `bson:"status"`       // 文章状态，如草稿、发布等
	AuthorID     int64     `bson:"authorid"`     // 用户uid
	Slug         string    `bson:"slug"`         // 文章的唯一标识，用于生成友好URL
	CategoryID   int64     `bson:"categoryid"`   // 关联分类表的外键
	PlateID      int64     `bson:"plateid"`      // 关联板块表的外键
	Plate        Plate     `bson:"plate"`        // 板块关系
	Tags         string    `bson:"tags"`         // 文章标签，以逗号分隔
	CommentCount int64     `bson:"commentcount"` // 文章的评论数量
}

func NewPostDAO(db *gorm.DB, l *zap.Logger, client *mongo.Client) PostDAO {
	return &postDAO{
		client: client,
		l:      l,
		db:     db,
	}
}

// 获取当前时间的时间戳
func (p *postDAO) getCurrentTime() int64 {
	return time.Now().UnixMilli()
}

// Insert 创建一个新的帖子记录
func (p *postDAO) Insert(ctx context.Context, post Post) (uint, error) {
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查 plate_id 是否存在
		var count int64
		if err := tx.Model(&Plate{}).Where("id = ?", post.PlateID).Count(&count).Error; err != nil {
			p.l.Error("failed to check plate existence", zap.Error(err))
			return err
		}
		if count == 0 {
			return errors.New("plate not found")
		}
		// 创建帖子
		if err := tx.Create(&post).Error; err != nil {
			p.l.Error("failed to insert post", zap.Error(err))
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return post.ID, nil
}

// UpdateById 通过Id更新帖子
func (p *postDAO) UpdateById(ctx context.Context, post Post) error {
	if post.ID == 0 || post.AuthorID == 0 {
		return ErrInvalidParams
	}

	res := p.db.WithContext(ctx).Model(&Post{}).Where("id = ? AND author_id = ?", post.ID, post.AuthorID).Updates(map[string]interface{}{
		"title":    post.Title,
		"content":  post.Content,
		"plate_id": post.PlateID,
		"status":   post.Status,
	})
	if res.Error != nil {
		p.l.Error("failed to update post", zap.Error(res.Error))
		return res.Error
	}

	if res.RowsAffected == 0 {
		return ErrPostNotFound
	}

	return nil
}

// UpdateStatus 更新帖子状态
func (p *postDAO) UpdateStatus(ctx context.Context, post Post) error {
	res := p.db.WithContext(ctx).Model(&Post{}).Where("id = ?", post.ID).Updates(map[string]interface{}{
		"status": post.Status,
	})
	if res.Error != nil {
		p.l.Error("failed to update post status", zap.Error(res.Error))
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPostNotFound
	}
	return nil
}

// GetById 根据ID获取一个帖子记录
func (p *postDAO) GetById(ctx context.Context, postId uint, uid int64) (Post, error) {
	var post Post
	err := p.db.WithContext(ctx).Where("author_id = ? AND id = ?", uid, postId).First(&post).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			p.l.Debug("post not found", zap.Uint("id", postId), zap.Int64("author_id", uid))
			return Post{}, ErrPostNotFound
		}
		p.l.Error("failed to get post", zap.Error(err))
		return Post{}, err
	}
	return post, nil
}

// List 查询作者帖子列表
func (p *postDAO) List(ctx context.Context, pagination domain.Pagination) ([]Post, error) {
	var posts []Post
	if err := p.db.WithContext(ctx).Where("author_id = ?", pagination.Uid).
		Limit(int(*pagination.Size)).Offset(int(*pagination.Offset)).Find(&posts).Error; err != nil {
		p.l.Error("find post list failed", zap.Error(err))
		return nil, err
	}
	return posts, nil
}

// GetPubById 根据ID获取一个已发布的帖子记录
func (p *postDAO) GetPubById(ctx context.Context, postId uint) (ListPubPost, error) {
	var post ListPubPost
	// 设置查询超时时间
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status := domain.Published
	// 设置查询过滤器，只查找状态为已发布的帖子
	filter := bson.M{
		"id":     postId,
		"status": status,
	}
	// 在MongoDB的posts集合中查找记录
	err := p.client.Database("linkme").Collection("posts").FindOne(ctx, filter).Decode(&post)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			p.l.Debug("published post not found", zap.Error(err))
			return ListPubPost{}, ErrPostNotFound
		}
		p.l.Error("failed to get published post", zap.Error(err))
		return ListPubPost{}, err
	}
	return post, nil
}

// ListPub 查询公开帖子列表
func (p *postDAO) ListPub(ctx context.Context, pagination domain.Pagination) ([]ListPubPost, error) {
	status := domain.Published
	// 设置查询超时时间
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// 指定数据库与集合
	collection := p.client.Database("linkme").Collection("posts")
	filter := bson.M{
		"status": status,
	}
	// 设置分页查询参数
	opts := options.FindOptions{
		Skip:  pagination.Offset,
		Limit: pagination.Size,
	}
	var posts []ListPubPost
	cursor, err := collection.Find(ctx, filter, &opts)
	if err != nil {
		p.l.Error("database query failed", zap.Error(err))
		return nil, err
	}
	// 将获取到的查询结果解码到posts结构体中
	if err = cursor.All(ctx, &posts); err != nil {
		p.l.Error("failed to decode query results", zap.Error(err))
		return nil, err
	}
	if len(posts) == 0 {
		p.l.Debug("query returned no results")
	}
	return posts, nil
}

// DeleteById 通过id删除帖子
func (p *postDAO) DeleteById(ctx context.Context, post Post) error {
	// 使用事务来确保操作的原子性
	tx := p.db.WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()
	// 更新帖子的状态为已删除，并使用 GORM 的软删除功能
	if err := tx.Model(&Post{}).Where("id = ?", post.ID).Updates(map[string]interface{}{
		"status": domain.Deleted,
	}).Delete(&Post{}).Error; err != nil {
		tx.Rollback()
		p.l.Error("failed to delete post", zap.Error(err))
		return err
	}
	// 提交事务
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		p.l.Error("failed to commit transaction", zap.Error(err))
		return err
	}
	return nil
}

// ListAllPost 查询所有未删除的帖子列表
func (p *postDAO) ListAllPost(ctx context.Context, pagination domain.Pagination) ([]Post, error) {
	var posts []Post
	if err := p.db.WithContext(ctx).Limit(int(*pagination.Size)).Offset(int(*pagination.Offset)).Find(&posts).Error; err != nil {
		p.l.Error("find post list failed", zap.Error(err))
		return nil, err
	}
	return posts, nil
}

// GetPost 根据ID获取一个未删除的帖子记录
func (p *postDAO) GetPost(ctx context.Context, postId uint) (Post, error) {
	var post Post
	err := p.db.WithContext(ctx).Where("id = ?", postId).First(&post).Error
	if err != nil {
		p.l.Error("find post failed", zap.Error(err))
		return Post{}, err
	}
	return post, nil
}

// GetPostCount 获取未删除的帖子数量
func (p *postDAO) GetPostCount(ctx context.Context) (int64, error) {
	var count int64
	if err := p.db.WithContext(ctx).Model(&Post{}).Count(&count).Error; err != nil {
		p.l.Error("failed to get post count", zap.Error(err))
		return 0, err
	}
	return count, nil
}
