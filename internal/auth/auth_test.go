package auth

import "testing"

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  User@Example.COM ": "user@example.com",
		"Foo@Bar.IO":          "foo@bar.io",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q)=%q want %q", in, got, want)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	bad := []string{"", "abc", "abc@", "@b.com", "nope"}
	for _, e := range bad {
		if ValidateEmail(e) == nil {
			t.Errorf("%q 应判定为无效 email", e)
		}
	}
	good := []string{"a@b.com", "user.name@host.io"}
	for _, e := range good {
		if ValidateEmail(e) != nil {
			t.Errorf("%q 应为有效 email", e)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == nil {
		t.Error("短密码应被拒")
	}
	if ValidatePassword("12345678") != nil {
		t.Error("8 位密码应通过")
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	pw := "supersecret"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if hash == pw {
		t.Error("哈希不应等于明文")
	}
	// 正确密码校验通过
	ok, err := VerifyPassword(pw, hash)
	if err != nil || !ok {
		t.Errorf("正确密码应校验通过: %v %v", ok, err)
	}
	// 错误密码校验失败
	ok2, _ := VerifyPassword("wrongpass", hash)
	if ok2 {
		t.Error("错误密码不应校验通过")
	}
	// 相同密码每次哈希不同（argon2id 带 salt）
	hash2, _ := HashPassword(pw)
	if hash == hash2 {
		t.Error("相同密码两次哈希应不同（随机 salt）")
	}
}

// TestValidateEmail_TooShort 验证长度不足 5 的邮箱被拒绝。
// 覆盖 ValidateEmail 中 len(email) < 5 分支。
func TestValidateEmail_TooShort(t *testing.T) {
	// "a@.c" 域名部分含 "." 但总长 4 < 5 → 触发长度分支
	if ValidateEmail("a@.c") == nil {
		t.Error(`"a@.c" 应判定为无效 email（长度不足）`)
	}
}
